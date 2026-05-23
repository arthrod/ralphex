package agentbus

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultHealthInterval is how often the supervisor pokes the active role to confirm it
// is still making progress, unless overridden.
const DefaultHealthInterval = 30 * time.Minute

// healthMessage is the nudge sent to the active role on each health tick.
const healthMessage = "health-check: are you still making progress on the current task? if stuck, hand off (worker: ask_oracle; oracle/inspector: to-orchestrator) instead of looping."

// Launcher is the side-effecting surface the supervisor drives: tmux sessions and the
// opencode resume/kill/health operations. Defined here (consumer side) so tests inject
// a fake; TmuxLauncher is the production implementation.
type Launcher interface {
	EnsureSessions(ctx context.Context, roleEnv map[Role][]string) error
	KillPane(ctx context.Context, r Role) error
	Resume(ctx context.Context, r Role, sessionID, message string) (newSessionID string, err error)
	SendHealth(ctx context.Context, r Role, message string) error
}

// Committer makes the per-handoff checkpoint commit that lets the orchestrator roll back.
type Committer interface {
	CheckpointAll(ctx context.Context, msg string) (hash string, committed bool, err error)
}

// Logf is an optional structured-ish logger; nil disables logging.
type Logf func(format string, args ...any)

// Supervisor tails the durable handoff log and, for each new transition, makes a
// checkpoint commit, stops the previous role (best-effort), and resumes the shared
// opencode session as the target role. It also fires a periodic health tick.
type Supervisor struct {
	launcher       Launcher
	git            Committer
	healthInterval time.Duration
	log            Logf
	macKey         []byte
}

// NewSupervisor builds a supervisor. A zero healthInterval falls back to the default.
// macKey is the in-memory HMAC key the AuthServer used to sign handoff lines; the
// supervisor verifies each line against it and rejects any line lacking a valid mac.
func NewSupervisor(launcher Launcher, git Committer, healthInterval time.Duration, log Logf, macKey []byte) *Supervisor {
	if healthInterval <= 0 {
		healthInterval = DefaultHealthInterval
	}
	return &Supervisor{launcher: launcher, git: git, healthInterval: healthInterval, log: log, macKey: macKey}
}

func (s *Supervisor) logf(format string, args ...any) {
	if s.log != nil {
		s.log(format, args...)
	}
}

// Watch processes the existing backlog, then blocks watching the handoff log for new
// transitions and firing health ticks until ctx is canceled.
func (s *Supervisor) Watch(ctx context.Context) error {
	if err := EnsureDir(); err != nil {
		return err
	}

	// register the watcher BEFORE the initial backlog scan so a handoff written during
	// startup is buffered as an event and reprocessed (processNew is idempotent on seq),
	// rather than being lost in the gap between scanning and watching.
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("create watcher: %w", err)
	}
	defer watcher.Close()
	if err := watcher.Add(Dir()); err != nil {
		return fmt.Errorf("watch %s: %w", Dir(), err)
	}

	if err := s.processNew(ctx); err != nil {
		s.logf("initial handoff processing: %v", err)
	}

	ticker := time.NewTicker(s.healthInterval)
	defer ticker.Stop()

	logName := filepath.Base(handoffsPath())
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("supervisor stopped: %w", ctx.Err())
		case ev, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if filepath.Base(ev.Name) == logName && ev.Op&(fsnotify.Write|fsnotify.Create) != 0 {
				if err := s.processNew(ctx); err != nil {
					s.logf("handoff processing: %v", err)
				}
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			s.logf("watcher error: %v", err)
		case <-ticker.C:
			s.sendHealth(ctx)
		}
	}
}

// processNew applies every handoff newer than the last processed sequence. It advances
// the persisted LastSeq only after a transition is fully applied, so a failure mid-way
// leaves the record unprocessed and the next call retries it (idempotent on seq).
func (s *Supervisor) processNew(ctx context.Context) error {
	st, err := LoadState()
	if err != nil {
		return err
	}
	pending, err := ReadHandoffsSince(st.LastSeq)
	if err != nil {
		return err
	}
	for _, h := range pending {
		// authenticity gate: only the supervisor's in-memory key produces a valid mac, so a
		// line written directly to the file (e.g. a worker forging an inspector->orchestrator
		// transition) fails here. treat it as consumed-and-ignored: skip it but advance past it
		// so it is not reprocessed forever.
		if !VerifyHandoffMac(h, s.macKey) {
			s.logf("rejected unsigned/forged handoff seq %d", h.Seq)
			st.LastSeq = h.Seq
			if err := SaveState(st); err != nil {
				return fmt.Errorf("persist skip of handoff seq %d: %w", h.Seq, err)
			}
			continue
		}
		if err := s.applyHandoff(ctx, &st, h); err != nil {
			return fmt.Errorf("apply handoff seq %d: %w", h.Seq, err)
		}
	}
	return nil
}

func (s *Supervisor) applyHandoff(ctx context.Context, st *State, h Handoff) error {
	if err := CheckTransition(h.From, h.To); err != nil {
		return err
	}

	msg := fmt.Sprintf("agentbus: checkpoint at handoff %d (%s -> %s)", h.Seq, h.From, h.To)
	if _, committed, err := s.git.CheckpointAll(ctx, msg); err != nil {
		return fmt.Errorf("checkpoint: %w", err)
	} else if committed {
		s.logf("checkpoint committed before %s -> %s", h.From, h.To)
	}

	if err := s.launcher.KillPane(ctx, h.From); err != nil {
		s.logf("kill %s pane: %v", h.From, err)
	}

	newSID, err := s.launcher.Resume(ctx, h.To, st.SessionID, h.Message)
	if err != nil {
		return fmt.Errorf("resume %s: %w", h.To, err)
	}

	st.ActiveRole = h.To
	st.SessionID = newSID
	st.LastSeq = h.Seq
	return SaveState(*st)
}

func (s *Supervisor) sendHealth(ctx context.Context) {
	st, err := LoadState()
	if err != nil {
		s.logf("health tick: load state: %v", err)
		return
	}
	if !st.ActiveRole.Valid() {
		return
	}
	if err := s.launcher.SendHealth(ctx, st.ActiveRole, healthMessage); err != nil {
		s.logf("health tick: %v", err)
	}
}
