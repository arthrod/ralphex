package agentbus

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"os"
)

// socket protocol actions.
const (
	actionAuth    = "auth"
	actionHandoff = "handoff"
)

// socketRequest is one newline-delimited JSON request sent over the auth socket. Each
// connection carries exactly one request and receives one response.
type socketRequest struct {
	Action         string     `json:"action"` // "auth" | "handoff"
	Tool           string     `json:"tool"`
	Token          string     `json:"token"`
	To             Role       `json:"to,omitempty"`
	Status         TaskStatus `json:"status,omitempty"`
	ConfirmCurrent string     `json:"confirm_current,omitempty"`
	Message        string     `json:"message,omitempty"`
}

// socketResponse is the supervisor's reply to a socketRequest.
type socketResponse struct {
	OK      bool     `json:"ok"`
	Error   string   `json:"error,omitempty"`
	Handoff *Handoff `json:"handoff,omitempty"`
}

// AuthServer is the supervisor-owned authority that holds the token registry and the
// HMAC key in memory (never on disk, never in any agent env) and serves all token
// validation and handoff appends over a unix-domain socket. Because it is the sole
// signer, a handoff line written directly to the file without a valid mac is rejected on
// read.
type AuthServer struct {
	registry map[string]string
	macKey   []byte
	listener net.Listener
}

// NewAuthServer mints a fresh random token per tool and a 32-byte random HMAC key, both
// held only in memory. No files are written.
func NewAuthServer() (*AuthServer, error) {
	reg := make(map[string]string, len(AllTools))
	for _, tool := range AllTools {
		tok, err := randomToken()
		if err != nil {
			return nil, err
		}
		reg[tool] = tok
	}
	macKey := make([]byte, 32)
	if _, err := rand.Read(macKey); err != nil {
		return nil, fmt.Errorf("generate mac key: %w", err)
	}
	return &AuthServer{registry: reg, macKey: macKey}, nil
}

// Registry returns a copy of the tool->token map, for building per-role tmux env.
func (s *AuthServer) Registry() map[string]string {
	out := make(map[string]string, len(s.registry))
	maps.Copy(out, s.registry)
	return out
}

// MacKey returns a copy of the HMAC key the supervisor uses to verify handoff lines.
func (s *AuthServer) MacKey() []byte {
	out := make([]byte, len(s.macKey))
	copy(out, s.macKey)
	return out
}

// Listen removes any stale socket file, binds the unix-domain socket, and restricts it
// to the owning user (0o600).
func (s *AuthServer) Listen(socketPath string) error {
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale socket: %w", err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", socketPath, err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("chmod socket: %w", err)
	}
	s.listener = ln
	return nil
}

// Serve runs the accept loop, handling one request per connection in its own goroutine.
// It closes the listener when ctx is canceled so Accept unblocks, and returns nil on a
// clean cancellation (the listener-closed error is expected at shutdown).
func (s *AuthServer) Serve(ctx context.Context) error {
	if s.listener == nil {
		return errors.New("serve: listener not initialized; call Listen first")
	}
	go func() {
		<-ctx.Done()
		_ = s.listener.Close()
	}()
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			// shutdown: ctx canceled (we closed the listener) or the listener is already
			// closed — both are the expected clean-stop path, not a failure.
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}
		go s.handle(conn)
	}
}

// Close shuts down the listener.
func (s *AuthServer) Close() error {
	if s.listener == nil {
		return nil
	}
	if err := s.listener.Close(); err != nil {
		return fmt.Errorf("close auth listener: %w", err)
	}
	return nil
}

// handle decodes one request, validates the credential, and dispatches by action.
func (s *AuthServer) handle(conn net.Conn) {
	defer conn.Close()
	var req socketRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = writeResponse(conn, socketResponse{OK: false, Error: "bad request"})
		return
	}
	_ = writeResponse(conn, s.process(req))
}

// process validates the token then performs the requested action. It is split out from
// handle so it can be unit-tested without a live socket.
func (s *AuthServer) process(req socketRequest) socketResponse {
	expected, ok := s.registry[req.Tool]
	if !ok || subtle.ConstantTimeCompare([]byte(req.Token), []byte(expected)) != 1 {
		return socketResponse{OK: false, Error: "auth failed"}
	}
	switch req.Action {
	case actionAuth:
		return socketResponse{OK: true}
	case actionHandoff:
		from := RoleForTool(req.Tool)
		if err := CheckTransition(from, req.To); err != nil {
			return socketResponse{OK: false, Error: err.Error()}
		}
		stored, err := AppendSignedHandoff(Handoff{
			From:           from,
			To:             req.To,
			Status:         req.Status,
			ConfirmCurrent: req.ConfirmCurrent,
			Message:        req.Message,
		}, s.macKey)
		if err != nil {
			return socketResponse{OK: false, Error: err.Error()}
		}
		return socketResponse{OK: true, Handoff: &stored}
	default:
		return socketResponse{OK: false, Error: "unknown action"}
	}
}

// writeResponse encodes a single response on the connection.
func writeResponse(conn net.Conn, resp socketResponse) error {
	if err := json.NewEncoder(conn).Encode(resp); err != nil {
		return fmt.Errorf("encode response: %w", err)
	}
	return nil
}

// dialAndSend connects to the supervisor's auth socket, sends one request, and reads one
// response. It is the shared client helper for Authenticate and SubmitHandoff.
func dialAndSend(req socketRequest) (socketResponse, error) {
	var d net.Dialer
	conn, err := d.DialContext(context.Background(), "unix", socketPath())
	if err != nil {
		return socketResponse{}, fmt.Errorf("dial auth socket: %w", err)
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return socketResponse{}, fmt.Errorf("send request: %w", err)
	}
	var resp socketResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return socketResponse{}, fmt.Errorf("read response: %w", err)
	}
	return resp, nil
}
