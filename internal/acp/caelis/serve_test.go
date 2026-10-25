package caelis

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
)

const testTimeout = 5 * time.Second

// fakeAgent is a protocol.Agent whose behaviour each test injects.
type fakeAgent struct {
	client protocol.Client

	initialize      func(context.Context, protocol.InitializeRequest) (protocol.InitializeResponse, error)
	newSession      func(context.Context, protocol.NewSessionRequest) (protocol.NewSessionResponse, error)
	loadSession     func(context.Context, protocol.LoadSessionRequest) (protocol.LoadSessionResponse, error)
	resumeSession   func(context.Context, protocol.ResumeSessionRequest) (protocol.ResumeSessionResponse, error)
	listSessions    func(context.Context, protocol.ListSessionsRequest) (protocol.ListSessionsResponse, error)
	deleteSession   func(context.Context, protocol.DeleteSessionRequest) error
	prompt          func(context.Context, protocol.PromptRequest) (protocol.PromptResponse, error)
	cancel          func(context.Context, protocol.CancelRequest) error
	setConfigOption func(context.Context, protocol.SetConfigOptionRequest) (protocol.SetConfigOptionResponse, error)
}

var _ protocol.Agent = (*fakeAgent)(nil)

func (f *fakeAgent) factory(c protocol.Client) protocol.Agent {
	f.client = c
	return f
}

func (f *fakeAgent) Initialize(ctx context.Context, r protocol.InitializeRequest) (protocol.InitializeResponse, error) {
	if f.initialize == nil {
		return protocol.InitializeResponse{ProtocolVersion: 1}, nil
	}
	return f.initialize(ctx, r)
}

func (f *fakeAgent) NewSession(ctx context.Context, r protocol.NewSessionRequest) (protocol.NewSessionResponse, error) {
	if f.newSession == nil {
		return protocol.NewSessionResponse{SessionID: "s1"}, nil
	}
	return f.newSession(ctx, r)
}

func (f *fakeAgent) LoadSession(ctx context.Context, r protocol.LoadSessionRequest) (protocol.LoadSessionResponse, error) {
	if f.loadSession == nil {
		return protocol.LoadSessionResponse{}, nil
	}
	return f.loadSession(ctx, r)
}

func (f *fakeAgent) ResumeSession(ctx context.Context, r protocol.ResumeSessionRequest) (protocol.ResumeSessionResponse, error) {
	if f.resumeSession == nil {
		return protocol.ResumeSessionResponse{}, nil
	}
	return f.resumeSession(ctx, r)
}

func (f *fakeAgent) ListSessions(ctx context.Context, r protocol.ListSessionsRequest) (protocol.ListSessionsResponse, error) {
	if f.listSessions == nil {
		return protocol.ListSessionsResponse{}, nil
	}
	return f.listSessions(ctx, r)
}

func (f *fakeAgent) DeleteSession(ctx context.Context, r protocol.DeleteSessionRequest) error {
	if f.deleteSession == nil {
		return nil
	}
	return f.deleteSession(ctx, r)
}

func (f *fakeAgent) Prompt(ctx context.Context, r protocol.PromptRequest) (protocol.PromptResponse, error) {
	if f.prompt == nil {
		return protocol.PromptResponse{StopReason: protocol.StopReasonEndTurn}, nil
	}
	return f.prompt(ctx, r)
}

func (f *fakeAgent) Cancel(ctx context.Context, r protocol.CancelRequest) error {
	if f.cancel == nil {
		return nil
	}
	return f.cancel(ctx, r)
}

func (f *fakeAgent) SetConfigOption(ctx context.Context, r protocol.SetConfigOptionRequest) (protocol.SetConfigOptionResponse, error) {
	if f.setConfigOption == nil {
		return protocol.SetConfigOptionResponse{}, nil
	}
	return f.setConfigOption(ctx, r)
}

// fakeClient is the editor side: it records what the agent sends and answers
// reverse requests from injected functions.
type fakeClient struct {
	mu                 sync.Mutex
	updates            []acp.SessionNotification
	permissionRequests []acp.RequestPermissionRequest
	elicitRequests     []acp.CreateElicitationRequest

	permission func(acp.RequestPermissionRequest) acp.RequestPermissionResponse
	elicit     func(acp.CreateElicitationRequest) acp.CreateElicitationResponse
}

var (
	_ acp.Client            = (*fakeClient)(nil)
	_ acp.ClientElicitation = (*fakeClient)(nil)
)

func (c *fakeClient) SessionUpdate(_ context.Context, n acp.SessionNotification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updates = append(c.updates, n)
	return nil
}

func (c *fakeClient) RequestPermission(_ context.Context, r acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	c.mu.Lock()
	c.permissionRequests = append(c.permissionRequests, r)
	c.mu.Unlock()
	return c.permission(r), nil
}

func (c *fakeClient) CreateElicitation(_ context.Context, r acp.CreateElicitationRequest) (acp.CreateElicitationResponse, error) {
	c.mu.Lock()
	c.elicitRequests = append(c.elicitRequests, r)
	c.mu.Unlock()
	return c.elicit(r), nil
}

func (c *fakeClient) receivedUpdates() []acp.SessionNotification {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]acp.SessionNotification(nil), c.updates...)
}

type pipes struct {
	toAgentR   *io.PipeReader
	toAgentW   *io.PipeWriter
	fromAgentR *io.PipeReader
	fromAgentW *io.PipeWriter
}

type served struct {
	done chan struct{}
	err  error
}

// serveOnPipes runs Serve against in-memory pipes. On cleanup it cancels the
// context and requires Serve to have returned without error.
func serveOnPipes(t *testing.T, newAgent func(protocol.Client) protocol.Agent) (pipes, *served) {
	t.Helper()
	var p pipes
	p.toAgentR, p.toAgentW = io.Pipe()
	p.fromAgentR, p.fromAgentW = io.Pipe()

	ctx, cancel := context.WithCancel(context.Background())
	s := &served{done: make(chan struct{})}
	go func() {
		defer close(s.done)
		s.err = Serve(ctx, p.toAgentR, p.fromAgentW, newAgent)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-s.done:
			require.NoError(t, s.err)
		case <-time.After(testTimeout):
			require.Fail(t, "Serve did not return after its context ended")
		}
	})
	return p, s
}

type harness struct {
	conn   *acp.ClientSideConnection
	client *fakeClient
	agent  *fakeAgent
	pipes  pipes
	served *served
}

func newHarness(t *testing.T, agent *fakeAgent) *harness {
	t.Helper()
	p, s := serveOnPipes(t, agent.factory)
	client := &fakeClient{}
	conn := acp.NewClientSideConnection(client, p.toAgentW, p.fromAgentR)
	t.Cleanup(func() { _ = conn.Close() })
	return &harness{conn: conn, client: client, agent: agent, pipes: p, served: s}
}

func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(testTimeout):
		require.FailNow(t, "timed out waiting for "+what)
		var zero T
		return zero
	}
}

func textPrompt(sessionID, text string) acp.PromptRequest {
	return acp.PromptRequest{
		SessionId: acp.SessionId(sessionID),
		Prompt:    []acp.ContentBlock{acp.TextBlock(text)},
	}
}

type promptResult struct {
	resp acp.PromptResponse
	err  error
}

// startPrompt sends a prompt from a goroutine and reports the outcome on the
// returned channel.
func (h *harness) startPrompt(ctx context.Context, req acp.PromptRequest) <-chan promptResult {
	out := make(chan promptResult, 1)
	go func() {
		resp, err := h.conn.Prompt(ctx, req)
		out <- promptResult{resp, err}
	}()
	return out
}

type rpcReply struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type rawConn struct {
	t *testing.T
	w io.Writer
	r *bufio.Reader
}

func newRawConn(t *testing.T, p pipes) *rawConn {
	return &rawConn{t: t, w: p.toAgentW, r: bufio.NewReader(p.fromAgentR)}
}

func (c *rawConn) send(frame string) {
	c.t.Helper()
	_, err := io.WriteString(c.w, frame+"\n")
	require.NoError(c.t, err)
}

func (c *rawConn) readReply() rpcReply {
	c.t.Helper()
	line := make(chan []byte, 1)
	go func() {
		b, _ := c.r.ReadBytes('\n')
		line <- b
	}()
	var reply rpcReply
	require.NoError(c.t, json.Unmarshal(recv(c.t, line, "a reply frame"), &reply))
	return reply
}

func TestServeInitializeWireFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		resp protocol.InitializeResponse
		want string
	}{
		{
			name: "every capability enabled",
			resp: protocol.InitializeResponse{
				ProtocolVersion: 1, AgentName: "angela", AgentVersion: "v1",
				LoadSession: true, ResumeSession: true, ListSessions: true, DeleteSessions: true,
				PromptImage: true, PromptEmbeddedContext: true,
			},
			want: `{
				"protocolVersion":1,
				"agentInfo":{"name":"angela","version":"v1"},
				"authMethods":[],
				"agentCapabilities":{
					"auth":{},
					"mcpCapabilities":{},
					"loadSession":true,
					"promptCapabilities":{"image":true,"embeddedContext":true},
					"sessionCapabilities":{"list":{},"delete":{},"resume":{}}
				}
			}`,
		},
		{
			name: "nothing advertised unless the adapter enables it",
			resp: protocol.InitializeResponse{ProtocolVersion: 1, AgentName: "angela", AgentVersion: "v1"},
			want: `{
				"protocolVersion":1,
				"agentInfo":{"name":"angela","version":"v1"},
				"authMethods":[],
				"agentCapabilities":{
					"auth":{},
					"mcpCapabilities":{},
					"promptCapabilities":{},
					"sessionCapabilities":{}
				}
			}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			agent := &fakeAgent{initialize: func(context.Context, protocol.InitializeRequest) (protocol.InitializeResponse, error) {
				return tc.resp, nil
			}}
			p, _ := serveOnPipes(t, agent.factory)
			raw := newRawConn(t, p)
			raw.send(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`)
			reply := raw.readReply()
			require.Nil(t, reply.Error)
			require.JSONEq(t, tc.want, string(reply.Result))
		})
	}
}

func TestServeSurvivesRequestsArrivingBeforeTheAgentExists(t *testing.T) {
	t.Parallel()

	agent := &fakeAgent{}
	frameConsumed := make(chan struct{})
	p, _ := serveOnPipes(t, func(c protocol.Client) protocol.Agent {
		// The SDK is already reading; hold the agent back until it has taken
		// the initialize frame off the wire.
		<-frameConsumed
		return agent.factory(c)
	})
	raw := newRawConn(t, p)
	go func() {
		_, _ = io.WriteString(raw.w, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}`+"\n")
		close(frameConsumed)
	}()

	reply := raw.readReply()
	require.Nil(t, reply.Error)
	require.JSONEq(t, `{
		"protocolVersion":1,"authMethods":[],"agentInfo":{"name":"","version":""},
		"agentCapabilities":{"auth":{},"mcpCapabilities":{},"promptCapabilities":{},"sessionCapabilities":{}}
	}`, string(reply.Result))
}

func TestServeInitializePassesElicitationSupport(t *testing.T) {
	t.Parallel()

	got := make(chan protocol.InitializeRequest, 1)
	h := newHarness(t, &fakeAgent{initialize: func(_ context.Context, r protocol.InitializeRequest) (protocol.InitializeResponse, error) {
		got <- r
		return protocol.InitializeResponse{ProtocolVersion: 1}, nil
	}})
	_, err := h.conn.Initialize(t.Context(), acp.InitializeRequest{
		ProtocolVersion: 1,
		ClientCapabilities: acp.ClientCapabilities{
			Elicitation: &acp.ElicitationCapabilities{Form: &acp.ElicitationFormCapabilities{}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, protocol.InitializeRequest{ProtocolVersion: 1, ElicitationForm: true}, recv(t, got, "initialize"))
}

func TestServeRoutesSessionRequests(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		loaded   protocol.LoadSessionRequest
		resumed  protocol.ResumeSessionRequest
		listed   protocol.ListSessionsRequest
		deleted  protocol.DeleteSessionRequest
		configed protocol.SetConfigOptionRequest
		created  protocol.NewSessionRequest
	)
	options := []protocol.ConfigOption{{
		ID: "model", Name: "Model", Category: protocol.ConfigCategoryModel, CurrentValue: "a",
		Values: []protocol.ConfigValue{{Value: "a", Name: "A"}, {Value: "b", Name: "B"}},
	}}
	h := newHarness(t, &fakeAgent{
		newSession: func(_ context.Context, r protocol.NewSessionRequest) (protocol.NewSessionResponse, error) {
			mu.Lock()
			defer mu.Unlock()
			created = r
			return protocol.NewSessionResponse{SessionID: "s1", ConfigOptions: options}, nil
		},
		loadSession: func(_ context.Context, r protocol.LoadSessionRequest) (protocol.LoadSessionResponse, error) {
			mu.Lock()
			defer mu.Unlock()
			loaded = r
			return protocol.LoadSessionResponse{ConfigOptions: options}, nil
		},
		resumeSession: func(_ context.Context, r protocol.ResumeSessionRequest) (protocol.ResumeSessionResponse, error) {
			mu.Lock()
			defer mu.Unlock()
			resumed = r
			return protocol.ResumeSessionResponse{ConfigOptions: options}, nil
		},
		listSessions: func(_ context.Context, r protocol.ListSessionsRequest) (protocol.ListSessionsResponse, error) {
			mu.Lock()
			defer mu.Unlock()
			listed = r
			return protocol.ListSessionsResponse{
				Sessions:   []protocol.SessionInfo{{SessionID: "s1", Cwd: "/p", Title: "T"}},
				NextCursor: "c2",
			}, nil
		},
		deleteSession: func(_ context.Context, r protocol.DeleteSessionRequest) error {
			mu.Lock()
			defer mu.Unlock()
			deleted = r
			return nil
		},
		setConfigOption: func(_ context.Context, r protocol.SetConfigOptionRequest) (protocol.SetConfigOptionResponse, error) {
			mu.Lock()
			defer mu.Unlock()
			configed = r
			return protocol.SetConfigOptionResponse{ConfigOptions: options}, nil
		},
	})
	ctx := t.Context()

	newResp, err := h.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: "/p", McpServers: []acp.McpServer{}})
	require.NoError(t, err)
	require.Equal(t, acp.SessionId("s1"), newResp.SessionId)
	require.Len(t, newResp.ConfigOptions, 1)
	require.Equal(t, acp.SessionConfigValueId("a"), newResp.ConfigOptions[0].Select.CurrentValue)

	loadResp, err := h.conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: "s1", Cwd: "/p", McpServers: []acp.McpServer{}})
	require.NoError(t, err)
	require.Len(t, loadResp.ConfigOptions, 1)

	resumeResp, err := h.conn.ResumeSession(ctx, acp.ResumeSessionRequest{SessionId: "s1", Cwd: "/p", McpServers: []acp.McpServer{}})
	require.NoError(t, err)
	require.Len(t, resumeResp.ConfigOptions, 1)

	cursor, cwd := "c1", "/p"
	listResp, err := h.conn.ListSessions(ctx, acp.ListSessionsRequest{Cursor: &cursor, Cwd: &cwd})
	require.NoError(t, err)
	require.Len(t, listResp.Sessions, 1)
	require.Equal(t, "T", *listResp.Sessions[0].Title)
	require.Equal(t, "c2", *listResp.NextCursor)

	_, err = h.conn.DeleteSession(ctx, acp.DeleteSessionRequest{SessionId: "s1"})
	require.NoError(t, err)

	setResp, err := h.conn.SetSessionConfigOption(ctx, acp.SetSessionConfigOptionRequest{
		ValueId: &acp.SetSessionConfigOptionValueId{SessionId: "s1", ConfigId: "model", Value: "b"},
	})
	require.NoError(t, err)
	require.Len(t, setResp.ConfigOptions, 1)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, protocol.NewSessionRequest{Cwd: "/p"}, created)
	require.Equal(t, protocol.LoadSessionRequest{SessionID: "s1", Cwd: "/p"}, loaded)
	require.Equal(t, protocol.ResumeSessionRequest{SessionID: "s1", Cwd: "/p"}, resumed)
	require.Equal(t, protocol.ListSessionsRequest{Cwd: "/p", Cursor: "c1"}, listed)
	require.Equal(t, protocol.DeleteSessionRequest{SessionID: "s1"}, deleted)
	require.Equal(t, protocol.SetConfigOptionRequest{SessionID: "s1", ConfigID: "model", Value: "b"}, configed)
}

// NOTE: The editor learns a session ID from the session/new response, so an
// update for that session sent before the response would be dropped.
func TestServeNewSessionWorkRunsAfterTheResponseIsWritten(t *testing.T) {
	t.Parallel()

	agent := &fakeAgent{}
	agent.newSession = func(context.Context, protocol.NewSessionRequest) (protocol.NewSessionResponse, error) {
		return protocol.NewSessionResponse{
			SessionID: "s1",
			AfterSent: func(ctx context.Context) {
				_ = agent.client.SessionUpdate(ctx, protocol.SessionNotification{
					SessionID: "s1",
					Update: protocol.AvailableCommandsUpdate{Commands: []protocol.AvailableCommand{
						{Name: "user:review", Description: "Review a file"},
					}},
				})
			},
		}, nil
	}
	p, _ := serveOnPipes(t, agent.factory)
	raw := newRawConn(t, p)

	raw.send(`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"/p","mcpServers":[]}}`)

	first := raw.readReply()
	require.Nil(t, first.Error)
	require.JSONEq(t, `{"sessionId":"s1"}`, string(first.Result))

	second := raw.readReply()
	require.Equal(t, "session/update", second.Method)
	require.JSONEq(t, `{
		"sessionId":"s1",
		"update":{"sessionUpdate":"available_commands_update","availableCommands":[
			{"name":"user:review","description":"Review a file"}
		]}
	}`, string(second.Params))
}

func TestServePromptDeliversEveryBlockType(t *testing.T) {
	t.Parallel()

	got := make(chan protocol.PromptRequest, 1)
	h := newHarness(t, &fakeAgent{prompt: func(_ context.Context, r protocol.PromptRequest) (protocol.PromptResponse, error) {
		got <- r
		return protocol.PromptResponse{StopReason: protocol.StopReasonMaxTokens}, nil
	}})
	resp, err := h.conn.Prompt(t.Context(), acp.PromptRequest{
		SessionId: "s1",
		Prompt: []acp.ContentBlock{
			acp.TextBlock("fix it"),
			acp.ImageBlock("aGk=", "image/png"),
			acp.ResourceLinkBlock("a.go", "file:///p/a.go"),
			acp.ResourceBlock(acp.EmbeddedResourceResource{
				TextResourceContents: &acp.TextResourceContents{Uri: "file:///p/b.go", Text: "package b"},
			}),
		},
	})
	require.NoError(t, err)
	require.Equal(t, acp.StopReasonMaxTokens, resp.StopReason)
	require.Equal(t, protocol.PromptRequest{
		SessionID: "s1",
		Prompt: []protocol.ContentBlock{
			protocol.TextBlock{Text: "fix it"},
			protocol.ImageBlock{MIMEType: "image/png", Data: []byte("hi")},
			protocol.ResourceLinkBlock{URI: "file:///p/a.go", Name: "a.go"},
			protocol.ResourceBlock{URI: "file:///p/b.go", Text: "package b"},
		},
	}, recv(t, got, "prompt"))
}

func TestServeRejectsAudioPromptWithoutCallingTheAgent(t *testing.T) {
	t.Parallel()

	called := make(chan struct{}, 1)
	h := newHarness(t, &fakeAgent{prompt: func(context.Context, protocol.PromptRequest) (protocol.PromptResponse, error) {
		called <- struct{}{}
		return protocol.PromptResponse{}, nil
	}})
	_, err := h.conn.Prompt(t.Context(), acp.PromptRequest{
		SessionId: "s1",
		Prompt:    []acp.ContentBlock{acp.AudioBlock("aGk=", "audio/wav")},
	})
	var re *acp.RequestError
	require.ErrorAs(t, err, &re)
	require.Equal(t, -32602, re.Code)
	require.Empty(t, called)
}

func TestServeCancelYieldsCancelledStopReason(t *testing.T) {
	t.Parallel()

	type outcome struct {
		ctxErr    error
		sessionID string
	}
	started := make(chan struct{})
	cancelCh := make(chan protocol.CancelRequest, 1)
	outcomes := make(chan outcome, 1)
	h := newHarness(t, &fakeAgent{
		prompt: func(ctx context.Context, _ protocol.PromptRequest) (protocol.PromptResponse, error) {
			close(started)
			req := <-cancelCh
			outcomes <- outcome{ctxErr: ctx.Err(), sessionID: req.SessionID}
			return protocol.PromptResponse{StopReason: protocol.StopReasonCancelled}, nil
		},
		cancel: func(_ context.Context, r protocol.CancelRequest) error {
			cancelCh <- r
			return nil
		},
	})
	ctx := t.Context()

	result := h.startPrompt(ctx, textPrompt("s1", "go"))
	recv(t, started, "the prompt to start")
	require.NoError(t, h.conn.Cancel(ctx, acp.CancelNotification{SessionId: "s1"}))

	res := recv(t, result, "the prompt response")
	require.NoError(t, res.err, "a cancelled turn must not surface as a JSON-RPC error")
	require.Equal(t, acp.StopReasonCancelled, res.resp.StopReason)

	got := recv(t, outcomes, "the prompt outcome")
	require.NoError(t, got.ctxErr, "session/cancel must not cancel the context passed to Prompt")
	require.Equal(t, "s1", got.sessionID)
}

func TestServeSecondPromptDoesNotInterruptTheFirst(t *testing.T) {
	t.Parallel()

	type finish struct {
		name   string
		ctxErr error
	}
	var ctxs sync.Map
	started := make(chan string, 2)
	finished := make(chan finish, 2)
	release := map[string]chan struct{}{"first": make(chan struct{}), "second": make(chan struct{})}
	h := newHarness(t, &fakeAgent{prompt: func(ctx context.Context, r protocol.PromptRequest) (protocol.PromptResponse, error) {
		name := r.Prompt[0].(protocol.TextBlock).Text
		ctxs.Store(name, ctx)
		started <- name
		<-release[name]
		finished <- finish{name, ctx.Err()}
		return protocol.PromptResponse{StopReason: protocol.StopReasonEndTurn}, nil
	}})
	ctx := t.Context()

	first := h.startPrompt(ctx, textPrompt("s1", "first"))
	require.Equal(t, "first", recv(t, started, "the first prompt"))
	second := h.startPrompt(ctx, textPrompt("s1", "second"))
	require.Equal(t, "second", recv(t, started, "the second prompt"))

	firstCtx, _ := ctxs.Load("first")
	require.NoError(t, firstCtx.(context.Context).Err(), "a new prompt for the same session must not cancel the running one")

	close(release["first"])
	close(release["second"])
	for _, res := range []promptResult{recv(t, first, "the first response"), recv(t, second, "the second response")} {
		require.NoError(t, res.err)
		require.Equal(t, acp.StopReasonEndTurn, res.resp.StopReason)
	}
	for range 2 {
		require.NoError(t, recv(t, finished, "a prompt to finish").ctxErr)
	}
}

func TestServeCancelRequestDoesNotCancelPrompt(t *testing.T) {
	t.Parallel()

	var promptCtx context.Context
	started := make(chan struct{})
	release := make(chan struct{})
	h := newHarness(t, &fakeAgent{prompt: func(ctx context.Context, _ protocol.PromptRequest) (protocol.PromptResponse, error) {
		promptCtx = ctx
		close(started)
		<-release
		return protocol.PromptResponse{StopReason: protocol.StopReasonEndTurn}, nil
	}})

	clientCtx, cancelClient := context.WithCancel(t.Context())
	result := h.startPrompt(clientCtx, textPrompt("s1", "go"))
	recv(t, started, "the prompt to start")
	cancelClient()

	res := recv(t, result, "the client call to give up")
	require.Error(t, res.err)
	require.Never(t, func() bool { return promptCtx.Err() != nil }, 200*time.Millisecond, 10*time.Millisecond,
		"$/cancel_request must not cancel the context passed to Prompt")
	close(release)
}

func TestServeClosingTheConnectionCancelsPromptAndReturnsNil(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	observed := make(chan error, 1)
	h := newHarness(t, &fakeAgent{prompt: func(ctx context.Context, _ protocol.PromptRequest) (protocol.PromptResponse, error) {
		close(started)
		<-ctx.Done()
		observed <- ctx.Err()
		return protocol.PromptResponse{}, ctx.Err()
	}})

	_ = h.startPrompt(t.Context(), textPrompt("s1", "go"))
	recv(t, started, "the prompt to start")
	require.NoError(t, h.pipes.toAgentW.Close())

	require.ErrorIs(t, recv(t, observed, "the prompt context to end"), context.Canceled)
	select {
	case <-h.served.done:
		require.NoError(t, h.served.err, "a peer closing its end is a normal shutdown")
	case <-time.After(testTimeout):
		require.Fail(t, "Serve did not return after the peer closed the connection")
	}
}

func TestServeAgentToEditorRoundTrips(t *testing.T) {
	t.Parallel()

	selected := map[string]acp.RequestPermissionResponse{
		"allow": {Outcome: acp.NewRequestPermissionOutcomeSelected("allow")},
		"deny":  {Outcome: acp.NewRequestPermissionOutcomeCancelled()},
	}
	pending := protocol.ToolCallStatusPending
	completed := protocol.ToolCallStatusCompleted
	line := uint32(7)
	updates := []protocol.SessionUpdate{
		protocol.UserMessageChunk{MessageID: "m0", Content: protocol.TextBlock{Text: "u"}},
		protocol.AgentMessageChunk{MessageID: "m1", Content: protocol.TextBlock{Text: "a"}},
		protocol.AgentThoughtChunk{MessageID: "m1", Content: protocol.TextBlock{Text: "t"}},
		protocol.ToolCallStart{
			ID: "t1", Name: "Read", Title: "Read a.go", Kind: protocol.ToolKindRead, Status: pending,
			Locations: []protocol.Location{{Path: "/a.go", Line: &line}},
		},
		protocol.ToolCallUpdate{ID: "t1", Status: &completed, Content: []protocol.ToolCallContent{
			protocol.ToolDiff{Path: "/a.go", OldText: new("x"), NewText: "y"},
			protocol.ToolContent{Block: protocol.ImageBlock{MIMEType: "image/png", Data: []byte("hi")}},
		}},
		protocol.Plan{Entries: []protocol.PlanEntry{{Content: "c", Priority: protocol.PlanPriorityMedium, Status: protocol.PlanStatusPending}}},
		protocol.SessionInfoUpdate{Title: new("Title")},
		protocol.UsageUpdate{Used: 1, Size: 2, Cost: &protocol.Cost{Amount: 0.25, Currency: "USD"}},
		protocol.ConfigOptionUpdate{ConfigOptions: []protocol.ConfigOption{{
			ID: "mode", Name: "Mode", CurrentValue: "a", Values: []protocol.ConfigValue{{Value: "a", Name: "A"}},
		}}},
	}
	wantKinds := []string{
		"user_message_chunk", "agent_message_chunk", "agent_thought_chunk", "tool_call",
		"tool_call_update", "plan", "session_info_update", "usage_update", "config_option_update",
	}

	type replies struct {
		permissions []protocol.PermissionResponse
		elicitions  []protocol.ElicitationResponse
	}
	got := make(chan replies, 1)
	agent := &fakeAgent{}
	agent.prompt = func(ctx context.Context, r protocol.PromptRequest) (protocol.PromptResponse, error) {
		for _, u := range updates {
			if err := agent.client.SessionUpdate(ctx, protocol.SessionNotification{SessionID: r.SessionID, Update: u}); err != nil {
				return protocol.PromptResponse{}, err
			}
		}
		var out replies
		for _, id := range []string{"allow", "deny"} {
			resp, err := agent.client.RequestPermission(ctx, protocol.PermissionRequest{
				SessionID: r.SessionID,
				ToolCall:  protocol.ToolCallUpdate{ID: "t1"},
				Options: []protocol.PermissionOption{
					{ID: "allow", Name: "Allow", Kind: protocol.PermissionKindAllowOnce},
					{ID: id, Name: id, Kind: protocol.PermissionKindRejectOnce},
				},
			})
			if err != nil {
				return protocol.PromptResponse{}, err
			}
			out.permissions = append(out.permissions, resp)
		}
		for range 3 {
			resp, err := agent.client.Elicit(ctx, protocol.ElicitationRequest{
				SessionID: r.SessionID, ToolCallID: "t1", Message: "Pick",
				Fields: []protocol.FormField{{Name: "q", Title: "Q", Kind: protocol.FieldKindText}},
			})
			if err != nil {
				return protocol.PromptResponse{}, err
			}
			out.elicitions = append(out.elicitions, resp)
		}
		got <- out
		return protocol.PromptResponse{StopReason: protocol.StopReasonEndTurn}, nil
	}
	h := newHarness(t, agent)
	var permissionCalls, elicitCalls int
	h.client.permission = func(r acp.RequestPermissionRequest) acp.RequestPermissionResponse {
		permissionCalls++
		if permissionCalls == 1 {
			return selected["allow"]
		}
		return selected["deny"]
	}
	h.client.elicit = func(acp.CreateElicitationRequest) acp.CreateElicitationResponse {
		elicitCalls++
		switch elicitCalls {
		case 1:
			return acp.CreateElicitationResponse{Accept: &acp.CreateElicitationAccept{Action: "accept", Content: map[string]any{"q": "a"}}}
		case 2:
			return acp.CreateElicitationResponse{Decline: &acp.CreateElicitationDecline{Action: "decline"}}
		default:
			return acp.CreateElicitationResponse{Cancel: &acp.CreateElicitationCancel{Action: "cancel"}}
		}
	}

	resp, err := h.conn.Prompt(t.Context(), textPrompt("s1", "go"))
	require.NoError(t, err)
	require.Equal(t, acp.StopReasonEndTurn, resp.StopReason)

	out := recv(t, got, "the reverse-request results")
	require.Equal(t, []protocol.PermissionResponse{{OptionID: "allow"}, {Cancelled: true}}, out.permissions)
	require.Equal(t, []protocol.ElicitationResponse{
		{Action: protocol.ElicitActionAccept, Values: map[string]any{"q": "a"}},
		{Action: protocol.ElicitActionDecline},
		{Action: protocol.ElicitActionCancel},
	}, out.elicitions)

	h.client.mu.Lock()
	require.Len(t, h.client.permissionRequests, 2)
	require.Equal(t, acp.ToolCallId("t1"), h.client.permissionRequests[0].ToolCall.ToolCallId)
	require.Equal(t, acp.PermissionOptionKindAllowOnce, h.client.permissionRequests[0].Options[0].Kind)
	require.Len(t, h.client.elicitRequests, 3)
	form := h.client.elicitRequests[0].Form
	require.NotNil(t, form)
	require.Equal(t, "Pick", form.Message)
	require.Equal(t, "form", form.Mode)
	h.client.mu.Unlock()

	// Notifications are handled on their own goroutine, so they may land
	// after the prompt response.
	require.Eventually(t, func() bool { return len(h.client.receivedUpdates()) == len(updates) },
		testTimeout, 5*time.Millisecond)
	var kinds []string
	for _, n := range h.client.receivedUpdates() {
		require.Equal(t, acp.SessionId("s1"), n.SessionId)
		encoded, err := json.Marshal(n.Update)
		require.NoError(t, err)
		var probe struct {
			SessionUpdate string `json:"sessionUpdate"`
		}
		require.NoError(t, json.Unmarshal(encoded, &probe))
		kinds = append(kinds, probe.SessionUpdate)
	}
	require.Equal(t, wantKinds, kinds, "updates must arrive in the order they were sent")

	toolUpdate := h.client.receivedUpdates()[4].Update.ToolCallUpdate
	require.NotNil(t, toolUpdate)
	require.Len(t, toolUpdate.Content, 2)
	require.Equal(t, "y", toolUpdate.Content[0].Diff.NewText)
	require.Equal(t, "x", *toolUpdate.Content[0].Diff.OldText)
	require.Equal(t, "aGk=", toolUpdate.Content[1].Content.Content.Image.Data)
}

func TestServeAnswersUnsupportedMethodsWithMethodNotFound(t *testing.T) {
	t.Parallel()

	h := newHarness(t, &fakeAgent{})
	ctx := t.Context()
	calls := map[string]func() error{
		"session/close": func() error {
			_, err := h.conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: "s1"})
			return err
		},
		"session/set_mode": func() error {
			_, err := h.conn.SetSessionMode(ctx, acp.SetSessionModeRequest{SessionId: "s1", ModeId: "m"})
			return err
		},
		"authenticate": func() error {
			_, err := h.conn.Authenticate(ctx, acp.AuthenticateRequest{MethodId: "x"})
			return err
		},
		"logout": func() error {
			_, err := h.conn.Logout(ctx, acp.LogoutRequest{})
			return err
		},
	}
	for method, call := range calls {
		var re *acp.RequestError
		require.ErrorAs(t, call(), &re, method)
		require.Equal(t, -32601, re.Code, method)
	}
}

func TestServeMapsAgentErrorsToJSONRPCCodes(t *testing.T) {
	t.Parallel()

	h := newHarness(t, &fakeAgent{
		newSession: func(context.Context, protocol.NewSessionRequest) (protocol.NewSessionResponse, error) {
			return protocol.NewSessionResponse{}, protocol.Error{Code: protocol.CodeNotFound, Message: "gone"}
		},
		loadSession: func(context.Context, protocol.LoadSessionRequest) (protocol.LoadSessionResponse, error) {
			return protocol.LoadSessionResponse{}, errors.New("boom")
		},
		deleteSession: func(context.Context, protocol.DeleteSessionRequest) error {
			return protocol.Error{Code: protocol.CodeInvalidParams, Message: "busy"}
		},
	})
	ctx := t.Context()

	_, err := h.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: "/p", McpServers: []acp.McpServer{}})
	var re *acp.RequestError
	require.ErrorAs(t, err, &re)
	require.Equal(t, -32002, re.Code)
	require.Equal(t, "gone", re.Message)

	_, err = h.conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: "s1", Cwd: "/p", McpServers: []acp.McpServer{}})
	require.ErrorAs(t, err, &re)
	require.Equal(t, -32603, re.Code)
	require.Equal(t, "boom", re.Message)

	_, err = h.conn.DeleteSession(ctx, acp.DeleteSessionRequest{SessionId: "s1"})
	require.ErrorAs(t, err, &re)
	require.Equal(t, -32602, re.Code)
	require.Equal(t, "busy", re.Message)
}
