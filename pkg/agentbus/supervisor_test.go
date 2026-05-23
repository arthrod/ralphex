package agentbus

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type resumeCall struct {
	role    Role
	session string
	message string
}

type fakeLauncher struct {
	mu          sync.Mutex
	killed      []Role
	resumes     []resumeCall
	healths     []Role
	resumeFunc  func(r Role, sid, msg string) (string, error)
	ensureErr   error
	ensureCount int
}

func (f *fakeLauncher) EnsureSessions(_ context.Context, _ map[Role][]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureCount++
	return f.ensureErr
}

func (f *fakeLauncher) KillPane(_ context.Context, r Role) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.killed = append(f.killed, r)
	return nil
}

func (f *fakeLauncher) Resume(_ context.Context, r Role, sid, msg string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resumes = append(f.resumes, resumeCall{role: r, session: sid, message: msg})
	if f.resumeFunc != nil {
		return f.resumeFunc(r, sid, msg)
	}
	if sid != "" {
		return sid, nil
	}
	return "sid-" + string(r), nil
}

func (f *fakeLauncher) SendHealth(_ context.Context, r Role, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.healths = append(f.healths, r)
	return nil
}

type fakeCommitter struct {
	mu     sync.Mutex
	calls  int
	msgs   []string
	err    error
	commit bool
}

func (c *fakeCommitter) CheckpointAll(_ context.Context, msg string) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.msgs = append(c.msgs, msg)
	if c.err != nil {
		return "", false, c.err
	}
	return "hash", c.commit, nil
}

func TestSupervisorProcessNew(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())

	_, err := AppendHandoff(Handoff{From: RoleWorker, To: RoleInspector, Message: "done", ConfirmCurrent: "task t1"})
	require.NoError(t, err)

	launcher := &fakeLauncher{}
	committer := &fakeCommitter{commit: true}
	sup := NewSupervisor(launcher, committer, 0, nil)

	require.NoError(t, sup.processNew(context.Background()))

	assert.Equal(t, 1, committer.calls)
	assert.Equal(t, []Role{RoleWorker}, launcher.killed)
	require.Len(t, launcher.resumes, 1)
	assert.Equal(t, RoleInspector, launcher.resumes[0].role)
	assert.Equal(t, "done", launcher.resumes[0].message)

	st, err := LoadState()
	require.NoError(t, err)
	assert.Equal(t, RoleInspector, st.ActiveRole)
	assert.Equal(t, 1, st.LastSeq)
	assert.Equal(t, "sid-inspector", st.SessionID)
}

func TestSupervisorSessionPropagates(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	_, err := AppendHandoff(Handoff{From: RoleWorker, To: RoleInspector, Message: "first"})
	require.NoError(t, err)
	_, err = AppendHandoff(Handoff{From: RoleInspector, To: RoleOrchestrator, Message: "second"})
	require.NoError(t, err)

	launcher := &fakeLauncher{resumeFunc: func(r Role, sid, _ string) (string, error) {
		if sid == "" {
			return "sid-shared", nil
		}
		return sid, nil
	}}
	sup := NewSupervisor(launcher, &fakeCommitter{}, 0, nil)
	require.NoError(t, sup.processNew(context.Background()))

	require.Len(t, launcher.resumes, 2)
	assert.Empty(t, launcher.resumes[0].session)
	assert.Equal(t, "sid-shared", launcher.resumes[1].session, "second handoff resumes the same session")

	st, err := LoadState()
	require.NoError(t, err)
	assert.Equal(t, 2, st.LastSeq)
	assert.Equal(t, RoleOrchestrator, st.ActiveRole)
}

func TestSupervisorResumeFailureRetries(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	_, err := AppendHandoff(Handoff{From: RoleWorker, To: RoleInspector, Message: "x"})
	require.NoError(t, err)

	fail := true
	launcher := &fakeLauncher{resumeFunc: func(_ Role, _, _ string) (string, error) {
		if fail {
			return "", errors.New("opencode unavailable")
		}
		return "sid-ok", nil
	}}
	sup := NewSupervisor(launcher, &fakeCommitter{}, 0, nil)

	// first attempt fails; LastSeq must not advance so the record is retried
	assert.Error(t, sup.processNew(context.Background()))
	st, err := LoadState()
	require.NoError(t, err)
	assert.Equal(t, 0, st.LastSeq)

	// recovery: the same durable record is reprocessed and now succeeds
	fail = false
	require.NoError(t, sup.processNew(context.Background()))
	st, err = LoadState()
	require.NoError(t, err)
	assert.Equal(t, 1, st.LastSeq)
}

func TestSupervisorRejectsBadTransition(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	// craft an illegal transition directly in the log
	_, err := AppendHandoff(Handoff{From: RoleWorker, To: RoleOrchestrator, Message: "x"})
	require.NoError(t, err)

	launcher := &fakeLauncher{}
	sup := NewSupervisor(launcher, &fakeCommitter{}, 0, nil)
	assert.Error(t, sup.processNew(context.Background()))
	assert.Empty(t, launcher.resumes)
}

func TestSupervisorSendHealth(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	require.NoError(t, SaveState(State{ActiveRole: RoleWorker}))

	launcher := &fakeLauncher{}
	sup := NewSupervisor(launcher, &fakeCommitter{}, 0, nil)
	sup.sendHealth(context.Background())
	assert.Equal(t, []Role{RoleWorker}, launcher.healths)
}

func TestSupervisorHealthNoActiveRole(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	launcher := &fakeLauncher{}
	sup := NewSupervisor(launcher, &fakeCommitter{}, 0, nil)
	sup.sendHealth(context.Background())
	assert.Empty(t, launcher.healths)
}

func TestNewSupervisorDefaultHealthInterval(t *testing.T) {
	sup := NewSupervisor(&fakeLauncher{}, &fakeCommitter{}, 0, nil)
	assert.Equal(t, DefaultHealthInterval, sup.healthInterval)
}

func (f *fakeLauncher) resumeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.resumes)
}

func (f *fakeLauncher) healthCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.healths)
}

func TestSupervisorWatch(t *testing.T) {
	t.Setenv("AGENTBUS_DIR", t.TempDir())
	// seed an active role so the health tick has someone to poke
	require.NoError(t, SaveState(State{ActiveRole: RoleWorker}))

	launcher := &fakeLauncher{}
	sup := NewSupervisor(launcher, &fakeCommitter{}, 15*time.Millisecond, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- sup.Watch(ctx) }()

	// a new handoff line should be picked up via fsnotify and resumed
	_, err := AppendHandoff(Handoff{From: RoleWorker, To: RoleInspector, Message: "go"})
	require.NoError(t, err)
	assert.Eventually(t, func() bool { return launcher.resumeCount() == 1 }, 2*time.Second, 10*time.Millisecond)

	// the health ticker should fire at least once
	assert.Eventually(t, func() bool { return launcher.healthCount() >= 1 }, 2*time.Second, 10*time.Millisecond)

	cancel()
	assert.ErrorIs(t, <-done, context.Canceled)
}
