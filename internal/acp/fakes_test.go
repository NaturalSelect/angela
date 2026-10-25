package acp

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/NaturalSelect/angela/internal/workspace"
)

// fakeWorkspace implements only what the adapter calls. The embedded
// interface is nil, so any other call panics and shows up in a test.
type fakeWorkspace struct {
	Workspace

	mu sync.Mutex

	cfg      *config.Config
	cwd      string
	active   workspace.ActiveAgent
	mode     permission.PermissionMode
	readyErr error
	editErr  error

	sessions map[string]session.Session
	messages map[string][]message.Message
	busy     map[string]bool
	nextID   int

	events chan pubsub.Event[tea.Msg]

	run func(ctx context.Context, sessionID, prompt string, atts []message.Attachment) error
	// onActive runs inside AgentActive, outside the lock, so a test can hold
	// a prompt in its setup.
	onActive func()

	customCommands []commands.CustomCommand
	skillEntries   []skills.CatalogEntry
	skillBodies    map[string][]byte
	mcpPrompts     []commands.MCPPrompt
	promptText     func(clientID, promptID string, args map[string]string) (string, error)

	// summarize, regenerateTitle and sideQuestion stand in for the agent's
	// own work. A nil one succeeds without doing anything.
	summarize       func(ctx context.Context, sessionID string) error
	regenerateTitle func(ctx context.Context, sessionID string) error
	sideQuestion    func(ctx context.Context, sessionID, question string) (string, error)
	initPrompt      string
	initErr         error
	initMarked      int

	deleted         []string
	trace           []string
	cancelled       []string
	edits           []config.ActiveAgentEdit
	granted         []permission.PermissionRequest
	grantedAlways   []permission.PermissionRequest
	denied          []permission.PermissionRequest
	deniedAlways    []permission.PermissionRequest
	answers         [][]question.Answer
	questionCancels int
	shutdowns       int
}

var _ Workspace = (*fakeWorkspace)(nil)

func newFakeWorkspace() *fakeWorkspace {
	return &fakeWorkspace{
		cfg:      &config.Config{},
		cwd:      testCwd,
		sessions: make(map[string]session.Session),
		messages: make(map[string][]message.Message),
		busy:     make(map[string]bool),
	}
}

func (f *fakeWorkspace) addSession(s session.Session) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions[s.ID] = s
}

func (f *fakeWorkspace) setMessages(sessionID string, msgs ...message.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages[sessionID] = msgs
}

func (f *fakeWorkspace) CreateSession(_ context.Context, title string) (session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	s := session.Session{ID: fmt.Sprintf("s%d", f.nextID), Title: title}
	f.sessions[s.ID] = s
	return s, nil
}

func (f *fakeWorkspace) GetSession(_ context.Context, id string) (session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[id]
	if !ok {
		return session.Session{}, fmt.Errorf("%w: %s", session.ErrSessionNotFound, id)
	}
	return s, nil
}

func (f *fakeWorkspace) ListSessions(context.Context) ([]session.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]session.Session, 0, len(f.sessions))
	for _, s := range f.sessions {
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeWorkspace) DeleteSession(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, id)
	f.deleted = append(f.deleted, id)
	return nil
}

func (f *fakeWorkspace) ListMessages(_ context.Context, id string) ([]message.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]message.Message(nil), f.messages[id]...), nil
}

func (f *fakeWorkspace) AgentActive(context.Context, string) (workspace.ActiveAgent, error) {
	f.mu.Lock()
	hook := f.onActive
	active := f.active
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return active, nil
}

func (f *fakeWorkspace) AgentEditActive(_ context.Context, _ string, edit config.ActiveAgentEdit) (workspace.ActiveAgent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.editErr != nil {
		return workspace.ActiveAgent{}, f.editErr
	}
	f.edits = append(f.edits, edit)
	return f.active, nil
}

func (f *fakeWorkspace) AgentReadyErr() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readyErr
}

func (f *fakeWorkspace) AgentIsSessionBusy(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busy[id]
}

func (f *fakeWorkspace) AgentRun(ctx context.Context, sessionID, prompt string, atts ...message.Attachment) error {
	if f.run == nil {
		return nil
	}
	return f.run(ctx, sessionID, prompt, atts)
}

// AgentBeginAccepted hands back no reservation, which the adapter must
// tolerate: the real one does the same for a session with no executor.
func (f *fakeWorkspace) AgentBeginAccepted(context.Context, string) *agent.AcceptedRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trace = append(f.trace, "begin")
	return nil
}

func (f *fakeWorkspace) AgentRunAccepted(ctx context.Context, _ *agent.AcceptedRun, sessionID, prompt string, atts ...message.Attachment) error {
	f.mu.Lock()
	f.trace = append(f.trace, "run")
	f.mu.Unlock()
	return f.AgentRun(ctx, sessionID, prompt, atts...)
}

func (f *fakeWorkspace) AgentCancel(sessionID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trace = append(f.trace, "cancel")
	f.cancelled = append(f.cancelled, sessionID)
}

func (f *fakeWorkspace) PermissionMode() permission.PermissionMode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

func (f *fakeWorkspace) PermissionSetMode(mode permission.PermissionMode) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mode = mode
}

func (f *fakeWorkspace) PermissionGrant(req permission.PermissionRequest) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.granted = append(f.granted, req)
	return true
}

func (f *fakeWorkspace) PermissionGrantPersistent(req permission.PermissionRequest) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grantedAlways = append(f.grantedAlways, req)
	return true
}

func (f *fakeWorkspace) PermissionDeny(req permission.PermissionRequest) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.denied = append(f.denied, req)
	return true
}

func (f *fakeWorkspace) PermissionDenyPersistent(req permission.PermissionRequest) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deniedAlways = append(f.deniedAlways, req)
	return true
}

func (f *fakeWorkspace) QuestionAnswer(answers []question.Answer) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answers = append(f.answers, answers)
	return true
}

func (f *fakeWorkspace) QuestionCancel() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.questionCancels++
	return true
}

func (f *fakeWorkspace) Config() *config.Config { return f.cfg }

func (f *fakeWorkspace) ListCustomCommands(context.Context) ([]commands.CustomCommand, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.customCommands), nil
}

func (f *fakeWorkspace) ListSkills(context.Context) ([]skills.CatalogEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.skillEntries), nil
}

func (f *fakeWorkspace) ReadSkill(_ context.Context, id string) ([]byte, skills.SkillReadResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, ok := f.skillBodies[id]
	if !ok {
		return nil, skills.SkillReadResult{}, fmt.Errorf("%w: %s", skills.ErrSkillNotFound, id)
	}
	var name string
	for _, e := range f.skillEntries {
		if e.ID == id {
			name = e.Name
		}
	}
	return body, skills.SkillReadResult{Name: name}, nil
}

func (f *fakeWorkspace) ListMCPPrompts(context.Context) ([]commands.MCPPrompt, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.mcpPrompts), nil
}

func (f *fakeWorkspace) GetMCPPrompt(clientID, promptID string, args map[string]string) (string, error) {
	f.mu.Lock()
	get := f.promptText
	f.mu.Unlock()
	if get == nil {
		return "", errors.New("no prompt text configured")
	}
	return get(clientID, promptID, args)
}

func (f *fakeWorkspace) setMCPPrompts(prompts ...commands.MCPPrompt) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mcpPrompts = prompts
}

func (f *fakeWorkspace) AgentSummarize(ctx context.Context, sessionID string) error {
	f.mu.Lock()
	f.trace = append(f.trace, "summarize")
	do := f.summarize
	f.mu.Unlock()
	if do == nil {
		return nil
	}
	return do(ctx, sessionID)
}

func (f *fakeWorkspace) AgentRegenerateTitle(ctx context.Context, sessionID string) error {
	f.mu.Lock()
	f.trace = append(f.trace, "title")
	do := f.regenerateTitle
	f.mu.Unlock()
	if do == nil {
		return nil
	}
	return do(ctx, sessionID)
}

func (f *fakeWorkspace) AgentAskSideQuestion(ctx context.Context, sessionID, question string) (string, error) {
	f.mu.Lock()
	f.trace = append(f.trace, "btw")
	do := f.sideQuestion
	f.mu.Unlock()
	if do == nil {
		return "", nil
	}
	return do(ctx, sessionID, question)
}

func (f *fakeWorkspace) InitializePrompt() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.initPrompt, f.initErr
}

func (f *fakeWorkspace) MarkProjectInitialized() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.initMarked++
	return nil
}

func (f *fakeWorkspace) WorkingDir() string { return f.cwd }

func (f *fakeWorkspace) Shutdown() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdowns++
}

func (f *fakeWorkspace) Events(ctx context.Context) <-chan pubsub.Event[tea.Msg] {
	ch := make(chan pubsub.Event[tea.Msg], 64)
	f.mu.Lock()
	f.events = ch
	f.mu.Unlock()
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch
}

// publish sends a service event the way the app wraps it.
func (f *fakeWorkspace) publish(payload tea.Msg) {
	f.mu.Lock()
	ch := f.events
	f.mu.Unlock()
	ch <- pubsub.Event[tea.Msg]{Type: pubsub.UpdatedEvent, Payload: payload}
}

func (f *fakeWorkspace) snapshot(read func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	read()
}

// fakeClient records everything sent to the editor, in order.
type fakeClient struct {
	mu  sync.Mutex
	log []any

	permission func(protocol.PermissionRequest) (protocol.PermissionResponse, error)
	elicit     func(context.Context, protocol.ElicitationRequest) (protocol.ElicitationResponse, error)
}

var _ protocol.Client = (*fakeClient)(nil)

func (c *fakeClient) SessionUpdate(_ context.Context, n protocol.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.log = append(c.log, n)
	return nil
}

func (c *fakeClient) RequestPermission(_ context.Context, req protocol.PermissionRequest) (protocol.PermissionResponse, error) {
	c.mu.Lock()
	c.log = append(c.log, req)
	handler := c.permission
	c.mu.Unlock()
	if handler == nil {
		return protocol.PermissionResponse{Cancelled: true}, nil
	}
	return handler(req)
}

func (c *fakeClient) Elicit(ctx context.Context, req protocol.ElicitationRequest) (protocol.ElicitationResponse, error) {
	c.mu.Lock()
	c.log = append(c.log, req)
	handler := c.elicit
	c.mu.Unlock()
	if handler == nil {
		return protocol.ElicitationResponse{Action: protocol.ElicitActionCancel}, nil
	}
	return handler(ctx, req)
}

func (c *fakeClient) entries() []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]any(nil), c.log...)
}

// updates returns the session updates sent for one session, in order.
func (c *fakeClient) updates(sessionID string) []protocol.SessionUpdate {
	var out []protocol.SessionUpdate
	for _, e := range c.entries() {
		if n, ok := e.(protocol.SessionNotification); ok && n.SessionID == sessionID {
			out = append(out, n.Update)
		}
	}
	return out
}

func (c *fakeClient) permissionRequests() []protocol.PermissionRequest {
	var out []protocol.PermissionRequest
	for _, e := range c.entries() {
		if r, ok := e.(protocol.PermissionRequest); ok {
			out = append(out, r)
		}
	}
	return out
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	require.Eventually(t, condition, 5*time.Second, 5*time.Millisecond)
}
