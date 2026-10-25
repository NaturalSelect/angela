package acp

import (
	"cmp"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/version"
)

const (
	sessionPageSize = 50
	defaultTitle    = "New Session"
	closeTimeout    = 3 * time.Second
)

// ClientCaps are the editor capabilities that must be known before the
// workspace is built, because the agent's tool set is fixed at that point.
type ClientCaps struct {
	Elicitation bool
}

// OpenWorkspace builds the workspace for cwd. It is slow: it opens the
// database and starts MCP servers.
type OpenWorkspace func(ctx context.Context, cwd string, caps ClientCaps) (Workspace, error)

func invalidParams(format string, args ...any) protocol.Error {
	return protocol.Error{Code: protocol.CodeInvalidParams, Message: fmt.Sprintf(format, args...)}
}

func notFound(format string, args ...any) protocol.Error {
	return protocol.Error{Code: protocol.CodeNotFound, Message: fmt.Sprintf(format, args...)}
}

func internalError(format string, args ...any) protocol.Error {
	return protocol.Error{Code: protocol.CodeInternal, Message: fmt.Sprintf(format, args...)}
}

// Agent implements protocol.Agent on top of one workspace.
//
// NOTE: An Agent serves at most one workspace and its cwd never changes.
// MCP state and the sandbox are process-wide, so one process cannot serve
// two projects.
type Agent struct {
	ctx    context.Context
	cancel context.CancelFunc
	client protocol.Client
	open   OpenWorkspace
	reg    *registry

	elicitForm      atomic.Bool
	compaction      atomic.Bool
	completionGrace time.Duration

	mu       sync.Mutex
	ws       Workspace
	cwd      string
	pumpDone <-chan struct{}
}

var _ protocol.Agent = (*Agent)(nil)

// NewAgent returns an Agent whose background work stops when ctx ends or
// Close is called.
func NewAgent(ctx context.Context, c protocol.Client, open OpenWorkspace) *Agent {
	ctx, cancel := context.WithCancel(ctx)
	return &Agent{
		ctx:             ctx,
		cancel:          cancel,
		client:          c,
		open:            open,
		reg:             newRegistry(),
		completionGrace: defaultCompletionGrace,
	}
}

func (a *Agent) current() (Workspace, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ws, a.cwd
}

// workspaceFor returns the workspace, building it on the first call.
//
// NOTE: The workspace is built with the connection context, not the request
// context. The SDK cancels the request context when the request returns,
// and the app treats the context it is given as its whole lifetime.
func (a *Agent) workspaceFor(cwd string) (Workspace, error) {
	if !filepath.IsAbs(cwd) {
		return nil, invalidParams("cwd must be an absolute path")
	}
	cwd = filepath.Clean(cwd)

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.ws != nil {
		if cwd != a.cwd {
			return nil, invalidParams("this agent serves %s and cannot serve %s", a.cwd, cwd)
		}
		return a.ws, nil
	}

	elicitation := a.elicitForm.Load()
	ws, err := a.open(a.ctx, cwd, ClientCaps{Elicitation: elicitation})
	if err != nil {
		return nil, internalError("open workspace: %v", err)
	}
	a.ws, a.cwd = ws, cwd
	a.pumpDone = startPump(a.ctx, ws, a.client, a.reg, cwd, elicitation, a.compaction.Load())
	return ws, nil
}

// Close stops background work and shuts the workspace down.
func (a *Agent) Close() {
	a.cancel()
	ws, _ := a.current()
	if ws == nil {
		return
	}
	select {
	case <-a.pumpDone:
	case <-time.After(closeTimeout):
	}
	ws.Shutdown()
}

func (a *Agent) Initialize(_ context.Context, req protocol.InitializeRequest) (protocol.InitializeResponse, error) {
	a.elicitForm.Store(req.ElicitationForm)
	a.compaction.Store(req.Compaction)
	return protocol.InitializeResponse{
		ProtocolVersion:       1,
		AgentName:             "angela",
		AgentVersion:          version.Version,
		LoadSession:           true,
		ResumeSession:         true,
		ListSessions:          true,
		DeleteSessions:        true,
		CloseSession:          true,
		PromptImage:           true,
		PromptEmbeddedContext: true,
	}, nil
}

func readyWorkspace(ws Workspace) error {
	if err := ws.AgentReadyErr(); err != nil {
		return internalError("no LLM provider is configured; run angela in a terminal once to set one up")
	}
	return nil
}

func (a *Agent) NewSession(ctx context.Context, req protocol.NewSessionRequest) (protocol.NewSessionResponse, error) {
	ws, err := a.workspaceFor(req.Cwd)
	if err != nil {
		return protocol.NewSessionResponse{}, err
	}
	if err := readyWorkspace(ws); err != nil {
		return protocol.NewSessionResponse{}, err
	}
	sess, err := ws.CreateSession(ctx, defaultTitle)
	if err != nil {
		return protocol.NewSessionResponse{}, internalError("create session: %v", err)
	}
	s := a.reg.add(sess.ID)
	return protocol.NewSessionResponse{
		SessionID:     sess.ID,
		ConfigOptions: a.configOptions(ctx, ws, sess.ID),
		AfterSent: func(ctx context.Context) {
			openCommands(ctx, a.client, s, commandsOf(ctx, ws))
		},
	}, nil
}

// LoadSession replays the whole history and then returns, as the protocol
// requires.
//
// NOTE: The replay runs on the request goroutine. The session is not
// registered yet, so the event pump cannot write to it concurrently.
func (a *Agent) LoadSession(ctx context.Context, req protocol.LoadSessionRequest) (protocol.LoadSessionResponse, error) {
	opts, err := a.restoreSession(ctx, req.SessionID, req.Cwd, true)
	if err != nil {
		return protocol.LoadSessionResponse{}, err
	}
	return protocol.LoadSessionResponse{ConfigOptions: opts}, nil
}

// ResumeSession picks a session up where it stopped without sending its
// history. The editor keeps the transcript it already has, so only the
// title, usage and plan are sent.
func (a *Agent) ResumeSession(ctx context.Context, req protocol.ResumeSessionRequest) (protocol.ResumeSessionResponse, error) {
	opts, err := a.restoreSession(ctx, req.SessionID, req.Cwd, false)
	if err != nil {
		return protocol.ResumeSessionResponse{}, err
	}
	return protocol.ResumeSessionResponse{ConfigOptions: opts}, nil
}

// restoreSession registers an existing session with the event pump. Either
// way the pump must know the messages that already exist, or it would send
// them to the editor again as if they were new.
func (a *Agent) restoreSession(ctx context.Context, sessionID, cwd string, replay bool) ([]protocol.ConfigOption, error) {
	ws, err := a.workspaceFor(cwd)
	if err != nil {
		return nil, err
	}
	if err := readyWorkspace(ws); err != nil {
		return nil, err
	}

	sess, err := ws.GetSession(ctx, sessionID)
	switch {
	case errors.Is(err, session.ErrSessionNotFound):
		return nil, notFound("session %s not found", sessionID)
	case err != nil:
		return nil, internalError("get session: %v", err)
	}
	if sess.ParentSessionID != "" {
		return nil, invalidParams("session %s belongs to another session", sess.ID)
	}
	if ws.AgentIsSessionBusy(sess.ID) {
		return nil, invalidParams("session %s is running", sess.ID)
	}

	a.reg.remove(sess.ID)
	msgs, err := ws.ListMessages(ctx, sess.ID)
	if err != nil {
		return nil, internalError("list messages: %v", err)
	}

	dc := DiffContext{Cwd: ws.WorkingDir(), ToolCall: indexToolCalls(msgs), Compaction: a.compaction.Load()}
	sent := make(map[string]message.Message, len(msgs))
	for _, m := range msgs {
		if replay {
			for _, u := range Diff(message.Message{}, m, dc) {
				if err := a.client.SessionUpdate(ctx, protocol.SessionNotification{SessionID: sess.ID, Update: u}); err != nil {
					return nil, internalError("replay history: %v", err)
				}
			}
		}
		sent[m.ID] = m
	}

	active, _ := ws.AgentActive(ctx, sess.ID)
	state := sessionStateUpdates(sess, active)
	for _, u := range state {
		if err := a.client.SessionUpdate(ctx, protocol.SessionNotification{SessionID: sess.ID, Update: u}); err != nil {
			return nil, internalError("send session state: %v", err)
		}
	}

	s := a.reg.addLoaded(sess.ID, sent, state)
	openCommands(ctx, a.client, s, commandsOf(ctx, ws))
	return a.configOptions(ctx, ws, sess.ID), nil
}

func (a *Agent) ListSessions(ctx context.Context, req protocol.ListSessionsRequest) (protocol.ListSessionsResponse, error) {
	cwd := req.Cwd
	if cwd == "" {
		ws, current := a.current()
		if ws == nil {
			return protocol.ListSessionsResponse{}, nil
		}
		cwd = current
	}
	ws, err := a.workspaceFor(cwd)
	if err != nil {
		return protocol.ListSessionsResponse{}, err
	}
	sessions, err := ws.ListSessions(ctx)
	if err != nil {
		return protocol.ListSessionsResponse{}, internalError("list sessions: %v", err)
	}
	page, next, err := paginateSessions(sessions, req.Cursor, sessionPageSize)
	if err != nil {
		return protocol.ListSessionsResponse{}, err
	}

	_, root := a.current()
	infos := make([]protocol.SessionInfo, 0, len(page))
	for _, s := range page {
		infos = append(infos, protocol.SessionInfo{
			SessionID: s.ID,
			Cwd:       root,
			Title:     s.Title,
			UpdatedAt: time.Unix(s.UpdatedAt, 0),
		})
	}
	return protocol.ListSessionsResponse{Sessions: infos, NextCursor: next}, nil
}

// paginateSessions orders sessions newest first, breaking ties by ID, and
// returns the page that follows cursor. The cursor names the last session of
// the previous page, so it stays valid when sessions are added or removed.
func paginateSessions(sessions []session.Session, cursor string, size int) ([]session.Session, string, error) {
	sorted := slices.Clone(sessions)
	slices.SortFunc(sorted, func(a, b session.Session) int {
		return cmp.Or(cmp.Compare(b.UpdatedAt, a.UpdatedAt), cmp.Compare(a.ID, b.ID))
	})

	if cursor != "" {
		updatedAt, id, err := decodeCursor(cursor)
		if err != nil {
			return nil, "", invalidParams("invalid cursor")
		}
		sorted = slices.DeleteFunc(sorted, func(s session.Session) bool {
			return cmp.Or(cmp.Compare(s.UpdatedAt, updatedAt), cmp.Compare(id, s.ID)) >= 0
		})
	}

	if len(sorted) <= size {
		return sorted, "", nil
	}
	page := sorted[:size]
	last := page[len(page)-1]
	return page, encodeCursor(last.UpdatedAt, last.ID), nil
}

func encodeCursor(updatedAt int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(updatedAt, 10) + ":" + id))
}

func decodeCursor(cursor string) (int64, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, "", err
	}
	stamp, id, ok := strings.Cut(string(raw), ":")
	if !ok || id == "" {
		return 0, "", errors.New("malformed cursor")
	}
	updatedAt, err := strconv.ParseInt(stamp, 10, 64)
	if err != nil {
		return 0, "", err
	}
	return updatedAt, id, nil
}

// DeleteSession succeeds when the session is already gone, as the protocol
// asks.
func (a *Agent) DeleteSession(ctx context.Context, req protocol.DeleteSessionRequest) error {
	ws, _ := a.current()
	if ws == nil {
		return invalidParams("no project is open yet; delete requests carry no cwd")
	}
	if ws.AgentIsSessionBusy(req.SessionID) {
		return invalidParams("session %s is running", req.SessionID)
	}
	if _, err := ws.GetSession(ctx, req.SessionID); errors.Is(err, session.ErrSessionNotFound) {
		return nil
	} else if err != nil {
		return internalError("get session: %v", err)
	}
	if err := ws.DeleteSession(ctx, req.SessionID); err != nil {
		return internalError("delete session: %v", err)
	}
	a.reg.remove(req.SessionID)
	return nil
}

// CloseSession stops the session's work and forgets it on this connection.
// The stored session stays, so the editor can load or resume it later.
//
// NOTE: The registry entry goes last. The event pump drops events for
// sessions it does not know, so a waiting prompt is ended here instead of
// waiting for a completion event that would never be delivered.
func (a *Agent) CloseSession(_ context.Context, req protocol.CloseSessionRequest) error {
	s, ok := a.reg.get(req.SessionID)
	if !ok {
		return nil
	}
	ws, _ := a.current()
	cancelTurn(ws, s)
	ws.AgentClearQueue(s.id)
	s.abandonTurns()
	a.reg.remove(s.id)
	return nil
}

func (a *Agent) SetConfigOption(ctx context.Context, req protocol.SetConfigOptionRequest) (protocol.SetConfigOptionResponse, error) {
	if _, ok := a.reg.get(req.SessionID); !ok {
		return protocol.SetConfigOptionResponse{}, notFound("session %s not found", req.SessionID)
	}
	ws, _ := a.current()
	change, err := parseConfigChange(a.configOptions(ctx, ws, req.SessionID), req.ConfigID, req.Value)
	if err != nil {
		return protocol.SetConfigOptionResponse{}, err
	}

	if change.mode != nil {
		ws.PermissionSetMode(*change.mode)
		a.broadcastConfig(ctx, ws, req.SessionID)
	}
	if !change.edit.IsZero() {
		if _, err := ws.AgentEditActive(ctx, req.SessionID, change.edit); err != nil {
			if errors.Is(err, agent.ErrAgentNotAvailable) || errors.Is(err, agent.ErrVariantNotAvailable) {
				return protocol.SetConfigOptionResponse{}, invalidParams("%v", err)
			}
			return protocol.SetConfigOptionResponse{}, internalError("change %s: %v", req.ConfigID, err)
		}
	}
	return protocol.SetConfigOptionResponse{ConfigOptions: a.configOptions(ctx, ws, req.SessionID)}, nil
}

// NOTE: The permission mode is process-wide, so every other session shows a
// stale value until it is told.
func (a *Agent) broadcastConfig(ctx context.Context, ws Workspace, except string) {
	for _, id := range a.reg.ids() {
		if id == except {
			continue
		}
		update := protocol.ConfigOptionUpdate{ConfigOptions: a.configOptions(ctx, ws, id)}
		_ = a.client.SessionUpdate(ctx, protocol.SessionNotification{SessionID: id, Update: update})
	}
}

func (a *Agent) configOptions(ctx context.Context, ws Workspace, sessionID string) []protocol.ConfigOption {
	active, _ := ws.AgentActive(ctx, sessionID)
	return buildConfigOptions(ws.Config(), active, ws.PermissionMode())
}
