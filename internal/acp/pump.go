package acp

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/skills"
)

const maxSessionDepth = 8

// pump turns workspace events into editor notifications for the whole life
// of the connection. Titles, for one, arrive after a turn has ended.
//
// NOTE: Only the pump goroutine writes liveSession.sent, started and
// lastState, and rootOf. The pump never waits for the editor to answer a
// request: permission and question round trips run on their own goroutines.
type pump struct {
	ws         Workspace
	client     protocol.Client
	reg        *registry
	cwd        string
	elicitForm bool
	compaction bool

	rootOf    map[string]string
	questions questionGuard
}

// startPump returns a channel that closes once the event stream has ended.
func startPump(ctx context.Context, ws Workspace, c protocol.Client, reg *registry, cwd string, elicitForm, compaction bool) <-chan struct{} {
	p := &pump{
		ws:         ws,
		client:     c,
		reg:        reg,
		cwd:        cwd,
		elicitForm: elicitForm,
		compaction: compaction,
		rootOf:     make(map[string]string),
	}
	events := ws.Events(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range events {
			p.handle(ctx, ev.Payload)
		}
	}()
	return done
}

// NOTE: Each service event arrives wrapped twice: the outer event is always
// an update, and the payload is the service's own event with the real type.
func (p *pump) handle(ctx context.Context, payload any) {
	switch e := payload.(type) {
	case pubsub.Event[message.Message]:
		p.onMessage(ctx, e)
	case pubsub.Event[session.Session]:
		p.onSession(ctx, e)
	case pubsub.Event[notify.RunComplete]:
		p.onRunComplete(ctx, e.Payload)
	case pubsub.Event[permission.PermissionRequest]:
		p.onPermission(ctx, e.Payload)
	case pubsub.Event[question.Request]:
		p.onQuestion(ctx, e.Payload)
	case pubsub.Event[question.Notification]:
		p.questions.dismissed(e.Payload.BatchID)
	case pubsub.Event[notify.Notification]:
		p.onNotice(ctx, e.Payload)
	case pubsub.Event[mcp.Event]:
		p.onMCP(ctx, e.Payload)
	case pubsub.Event[skills.Event]:
		p.onSkills(ctx)
	}
}

// onMCP re-sends the command list when a server connects, drops, or changes
// its prompts. A server that connects does not announce its prompts as a
// separate event, so the state change covers that too.
//
// NOTE: Tool and resource changes do not affect the list.
func (p *pump) onMCP(ctx context.Context, e mcp.Event) {
	if e.Type != mcp.EventStateChanged && e.Type != mcp.EventPromptsListChanged {
		return
	}
	for _, s := range p.reg.all() {
		refreshCommands(ctx, p.client, s, commandsOf(ctx, p.ws))
	}
}

// onSkills re-sends the command list after the skill set changes, so a
// reload shows up in the editor's slash menu.
func (p *pump) onSkills(ctx context.Context) {
	for _, s := range p.reg.all() {
		refreshCommands(ctx, p.client, s, commandsOf(ctx, p.ws))
	}
}

func (p *pump) send(ctx context.Context, sessionID string, u protocol.SessionUpdate) {
	err := p.client.SessionUpdate(ctx, protocol.SessionNotification{SessionID: sessionID, Update: u})
	if err != nil {
		slog.Warn("Failed to send session update", "session_id", sessionID, "error", err)
	}
}

// NOTE: User messages are skipped: the editor already shows the prompt it
// sent, and a deleted message cannot be taken back from the editor. The one
// deletion that matters is a summary the user cancelled, which the editor
// has been told is in progress.
func (p *pump) onMessage(ctx context.Context, e pubsub.Event[message.Message]) {
	m := e.Payload
	if e.Type == pubsub.DeletedEvent {
		p.onSummaryDeleted(ctx, m)
		return
	}
	if m.Role == message.User {
		return
	}
	s, ok := p.route(ctx, m.SessionID)
	if !ok {
		return
	}
	p.emit(ctx, s, m)
}

func (p *pump) onSummaryDeleted(ctx context.Context, m message.Message) {
	if !p.compaction || !m.IsSummaryMessage {
		return
	}
	s, ok := p.route(ctx, m.SessionID)
	if !ok || s.id != m.SessionID {
		return
	}
	prev, seen := s.sent[m.ID]
	if !seen || prev.IsFinished() {
		return
	}
	delete(s.sent, m.ID)
	p.send(ctx, s.id, protocol.CompactionUpdate{ID: m.ID, Status: protocol.CompactionCancelled})
}

// emit is the only place that sends message updates, which keeps them in
// the order they were produced.
//
// NOTE: A sub-agent's tool calls reach the editor under prefixed IDs, so a
// permission request from the sub-agent points at a call the editor has
// seen. Its text and reasoning are dropped to keep them out of the answer.
func (p *pump) emit(ctx context.Context, s *liveSession, m message.Message) {
	child := m.SessionID != s.id
	dc := DiffContext{
		Cwd:        p.cwd,
		Compaction: p.compaction,
		ToolCall: func(id string) (message.ToolCall, bool) {
			return s.lookupCall(m.SessionID, id)
		},
	}
	for _, u := range Diff(s.sent[m.ID], m, dc) {
		if child {
			var ok bool
			if u, ok = prefixToolCall(u, m.SessionID); !ok {
				continue
			}
		}
		if start, ok := u.(protocol.ToolCallStart); ok {
			s.started[start.ID] = struct{}{}
		}
		p.send(ctx, s.id, u)
	}
	s.sent[m.ID] = m
}

func childToolCallID(childSessionID, toolCallID string) string {
	return childSessionID + "/" + toolCallID
}

func prefixToolCall(u protocol.SessionUpdate, childSessionID string) (protocol.SessionUpdate, bool) {
	switch u := u.(type) {
	case protocol.ToolCallStart:
		u.ID = childToolCallID(childSessionID, u.ID)
		return u, true
	case protocol.ToolCallUpdate:
		u.ID = childToolCallID(childSessionID, u.ID)
		return u, true
	default:
		return nil, false
	}
}

// reconcile re-reads a session's messages from the database and sends what
// the editor has not seen. Message events can be dropped or arrive late, but
// the database always has the messages first.
//
// NOTE: Repeating it is harmless, because an unchanged message yields no
// update.
func (p *pump) reconcile(ctx context.Context, s *liveSession, sessionID string) {
	msgs, err := p.ws.ListMessages(ctx, sessionID)
	if err != nil {
		slog.Warn("Failed to list messages for reconcile", "session_id", sessionID, "error", err)
		return
	}
	for _, m := range msgs {
		if m.Role != message.User {
			p.emit(ctx, s, m)
		}
	}
}

// onRunComplete sends whatever the editor missed and only then wakes the
// waiting prompt, so every update precedes its prompt response.
func (p *pump) onRunComplete(ctx context.Context, rc notify.RunComplete) {
	s, ok := p.reg.get(rc.SessionID)
	if !ok {
		return
	}
	t, ok := s.waiter(rc.RunID)
	if !ok {
		return
	}
	p.reconcile(ctx, s, s.id)
	for _, child := range p.childrenOf(s.id) {
		p.reconcile(ctx, s, child)
	}
	final := s.sent[rc.MessageID]
	t.deliver(turnEnd{rc: rc, finish: final.FinishReason()})
}

func (p *pump) childrenOf(rootID string) []string {
	var children []string
	for id, root := range p.rootOf {
		if root == rootID && id != rootID {
			children = append(children, id)
		}
	}
	return children
}

// route finds the registered session an event belongs to, walking up from
// a sub-agent session to its root. Events for other sessions are dropped.
func (p *pump) route(ctx context.Context, sessionID string) (*liveSession, bool) {
	if s, ok := p.reg.get(sessionID); ok {
		return s, true
	}
	root, ok := p.rootOf[sessionID]
	if !ok {
		if root = p.findRoot(ctx, sessionID); root == "" {
			return nil, false
		}
		p.rootOf[sessionID] = root
	}
	return p.reg.get(root)
}

// findRoot returns "" when the chain cannot be followed. Failures are not
// cached, because a session can appear after its first event.
func (p *pump) findRoot(ctx context.Context, sessionID string) string {
	id := sessionID
	for range maxSessionDepth {
		sess, err := p.ws.GetSession(ctx, id)
		if err != nil {
			return ""
		}
		if sess.ParentSessionID == "" {
			return sess.ID
		}
		id = sess.ParentSessionID
	}
	return ""
}

// NOTE: Only root sessions report state. A sub-agent's cost is already
// added to its parent, and every step publishes a session update, so
// repeats are filtered out.
func (p *pump) onSession(ctx context.Context, e pubsub.Event[session.Session]) {
	if e.Type == pubsub.DeletedEvent {
		return
	}
	s, ok := p.reg.get(e.Payload.ID)
	if !ok {
		return
	}
	active, _ := p.ws.AgentActive(ctx, s.id)
	for _, u := range sessionStateUpdates(e.Payload, active) {
		if s.alreadySent(u) {
			continue
		}
		p.send(ctx, s.id, u)
		s.remember(u)
	}
}

// NOTE: These notices carry no session, so they go to every session with a
// running turn. The editor is told to restart, because a login finished in
// another terminal cannot wake this process.
func (p *pump) onNotice(ctx context.Context, n notify.Notification) {
	var text string
	switch n.Type {
	case notify.TypeReAuthenticate:
		text = fmt.Sprintf("Your %s login has expired. Run `angela login` in a terminal, then restart the agent in your editor.", cmp.Or(n.ProviderID, "provider"))
	case notify.TypeAWSSSOAuth:
		text = "Refreshing AWS SSO login."
		if n.AWSSOURL != "" {
			text += " Complete it in your browser: " + n.AWSSOURL
		}
	default:
		return
	}
	for _, s := range p.reg.all() {
		if s.hasTurns() {
			p.send(ctx, s.id, protocol.AgentMessageChunk{Content: protocol.TextBlock{Text: text}})
		}
	}
}
