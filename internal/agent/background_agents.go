package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/session"
)

// maxBackgroundAgentsPerSession bounds how many background agents one
// session may have running at once.
const maxBackgroundAgentsPerSession = 8

var (
	errBackgroundAgentLimit = fmt.Errorf(
		"too many background agents are running in this session (limit %d); wait for one to finish or stop one with AgentStop",
		maxBackgroundAgentsPerSession,
	)
	errBackgroundAgentsClosed = errors.New("background agents are unavailable: the application is shutting down")
)

// backgroundAgentsAvailable is the one gate for the whole feature. The
// Agent schema and description, the AgentStatus and AgentStop tools, and
// the dispatch branch all read it, so they cannot disagree.
//
// Only a top-level turn of an interactive coordinator qualifies: a
// subagent has no user to wait on, and `angela run` exits when its turn
// does, which would strand the agent and its notice. A coordinator built
// without the registry and the inbox has nowhere to track or report an
// agent, so it does not offer the feature either.
func (c *coordinator) backgroundAgentsAvailable(depth int) bool {
	return depth == 0 && c.interactive && c.background != nil && c.notices != nil
}

// backgroundAgentInfo is a point-in-time view of one background agent.
type backgroundAgentInfo struct {
	ID              string
	ParentSessionID string
	ChildSessionID  string
	AgentType       string
	Task            string
	Status          tools.BackgroundAgentStatus
	StartedAt       time.Time
	FinishedAt      time.Time
}

type backgroundAgentEntry struct {
	info   backgroundAgentInfo
	cancel context.CancelFunc
}

// backgroundAgents is the coordinator's registry of agents launched with
// run_in_background. Entries outlive their run so a status query can still
// answer afterwards, and are dropped with the parent session.
//
// Once shut down it launches nothing and reports nothing, so agents that
// unwind during shutdown do not wake a session that is going away.
//
// The registry is nil-safe for the calls the coordinator makes without
// knowing whether anything was ever launched.
type backgroundAgents struct {
	mu     sync.Mutex
	byID   map[string]*backgroundAgentEntry
	closed bool
}

func newBackgroundAgents() *backgroundAgents {
	return &backgroundAgents{byID: make(map[string]*backgroundAgentEntry)}
}

// start registers a running agent. cancel stops the agent's run.
func (r *backgroundAgents) start(info backgroundAgentInfo, cancel context.CancelFunc) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return errBackgroundAgentsClosed
	}
	if _, exists := r.byID[info.ID]; exists {
		return fmt.Errorf("background agent %s was already launched", info.ID)
	}
	if r.runningLocked(info.ParentSessionID) >= maxBackgroundAgentsPerSession {
		return errBackgroundAgentLimit
	}
	info.Status = tools.BackgroundAgentRunning
	r.byID[info.ID] = &backgroundAgentEntry{info: info, cancel: cancel}
	return nil
}

func (r *backgroundAgents) runningLocked(parentSessionID string) int {
	var n int
	for _, e := range r.byID {
		if e.info.ParentSessionID == parentSessionID && e.info.Status == tools.BackgroundAgentRunning {
			n++
		}
	}
	return n
}

// abandon drops an agent whose launch failed before it ever ran.
func (r *backgroundAgents) abandon(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
}

// finish records the terminal status of a running agent. It reports false
// when nobody is left to be told: the agent is no longer registered
// because its parent session was deleted, or the registry was shut down.
func (r *backgroundAgents) finish(id string, status tools.BackgroundAgentStatus) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byID[id]
	if !ok {
		return false
	}
	e.info.Status = status
	e.info.FinishedAt = time.Now()
	return !r.closed
}

// get returns the agent id launched from parentSessionID. Another
// session's agent is reported as absent.
func (r *backgroundAgents) get(parentSessionID, id string) (backgroundAgentInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byID[id]
	if !ok || e.info.ParentSessionID != parentSessionID {
		return backgroundAgentInfo{}, false
	}
	return e.info, true
}

// list returns every agent launched from parentSessionID, oldest first.
func (r *backgroundAgents) list(parentSessionID string) []backgroundAgentInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	var infos []backgroundAgentInfo
	for _, e := range r.byID {
		if e.info.ParentSessionID == parentSessionID {
			infos = append(infos, e.info)
		}
	}
	slices.SortFunc(infos, func(a, b backgroundAgentInfo) int {
		if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return infos
}

// running returns the agents of parentSessionID that have not finished.
func (r *backgroundAgents) running(parentSessionID string) []backgroundAgentInfo {
	if r == nil {
		return nil
	}
	all := r.list(parentSessionID)
	return slices.DeleteFunc(all, func(i backgroundAgentInfo) bool {
		return i.Status != tools.BackgroundAgentRunning
	})
}

type stopOutcome int

const (
	stopUnknown stopOutcome = iota
	stopNotRunning
	stopRequested
)

// stop asks a running agent of parentSessionID to end. The agent reports
// its own terminal status once its run unwinds.
func (r *backgroundAgents) stop(parentSessionID, id string) stopOutcome {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.byID[id]
	if !ok || e.info.ParentSessionID != parentSessionID {
		return stopUnknown
	}
	if e.info.Status != tools.BackgroundAgentRunning {
		return stopNotRunning
	}
	e.cancel()
	return stopRequested
}

// stopChild ends the running agent whose sub-session is childSessionID and
// reports whether there was one. It is how a cancel aimed at the
// sub-session itself, such as the user's stop in the sub-session list,
// reaches the agent: cancelling through the executor alone would do nothing
// before the run registers as active.
func (r *backgroundAgents) stopChild(childSessionID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.byID {
		if e.info.ChildSessionID == childSessionID && e.info.Status == tools.BackgroundAgentRunning {
			e.cancel()
			return true
		}
	}
	return false
}

// shutdown ends every running agent and refuses new ones.
func (r *backgroundAgents) shutdown() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	for _, e := range r.byID {
		if e.info.Status == tools.BackgroundAgentRunning {
			e.cancel()
		}
	}
}

// sessionDeleted ends what a deleted session leaves behind. Deleting a
// parent drops its agents outright. Deleting only a child stops that one
// agent and keeps its entry, because the parent is still there to be told.
func (r *backgroundAgents) sessionDeleted(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, e := range r.byID {
		switch sessionID {
		case e.info.ParentSessionID:
			if e.info.Status == tools.BackgroundAgentRunning {
				e.cancel()
			}
			delete(r.byID, id)
		case e.info.ChildSessionID:
			if e.info.Status == tools.BackgroundAgentRunning {
				e.cancel()
			}
		}
	}
}

// launchBackgroundAgent starts params as a background agent and returns a
// receipt at once. The agent runs on a context detached from the launching
// tool call, so its parent's turn ending or being cancelled does not take
// it down. Only a stop, the deletion of one of its sessions, or shutdown
// does.
func (c *coordinator) launchBackgroundAgent(ctx context.Context, params subAgentParams, agentType, description string) tools.Result {
	if err := ctx.Err(); err != nil {
		return tools.Fail(err.Error())
	}

	agentID := tools.AgentID(c.sessions, params.AgentMessageID, params.ToolCallID)
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	info := backgroundAgentInfo{
		ID:              agentID,
		ParentSessionID: params.SessionID,
		ChildSessionID:  c.sessions.CreateAgentToolSessionID(params.AgentMessageID, params.ToolCallID),
		AgentType:       agentType,
		Task:            description,
		StartedAt:       time.Now(),
	}

	// NOTE: The slot is claimed before the sub-session exists, so a launch
	// refused for the limit leaves nothing behind to clean up.
	if err := c.background.start(info, cancel); err != nil {
		cancel()
		return tools.Fail(err.Error())
	}
	sess, err := c.prepareSubAgentSession(ctx, params)
	if err != nil {
		cancel()
		c.background.abandon(agentID)
		return tools.FailErr("create session", err)
	}

	go c.runBackgroundAgent(runCtx, cancel, sess, params, info)

	return tools.Ok(tools.BackgroundAgentLaunchReceipt(agentID)).
		WithMetadata(tools.AgentResponseMetadata{Background: true, AgentID: agentID})
}

// runBackgroundAgent runs a launched agent to the end, records how it
// ended, and tells the session that launched it.
func (c *coordinator) runBackgroundAgent(ctx context.Context, cancel context.CancelFunc, sess session.Session, params subAgentParams, info backgroundAgentInfo) {
	defer cancel()

	outcome := c.executeSubAgent(ctx, sess, params)
	status := classifyBackgroundOutcome(ctx, outcome)
	if !c.background.finish(info.ID, status) {
		return
	}

	banner := tools.ReportBanner(tools.ReportID(c.sessions, params.AgentMessageID, params.ToolCallID), info.AgentType, info.Task)
	notice := backgroundAgentNotice(info, status, outcome, banner)
	// NOTE: Post comes before the wake. A parent that is still finishing a
	// turn picks the notice up at its own end-of-turn check, and one that is
	// already idle is woken here, so no ordering strands the notice.
	c.notices.Post(params.SessionID, tools.RenderBackgroundAgentNotice(notice))
	c.wakeForNotices(params.SessionID)
}

func backgroundAgentNotice(info backgroundAgentInfo, status tools.BackgroundAgentStatus, outcome subAgentOutcome, banner string) tools.BackgroundAgentNotice {
	label := info.Task
	if label == "" {
		label = info.AgentType
	}
	notice := tools.BackgroundAgentNotice{AgentID: info.ID, Status: status}
	switch status {
	case tools.BackgroundAgentCompleted:
		notice.Summary = fmt.Sprintf("Agent %q completed", label)
		notice.Result = banner + outcome.Text
	case tools.BackgroundAgentFailed:
		notice.Summary = fmt.Sprintf("Agent %q failed", label)
		notice.Result = outcome.Failure
	default:
		notice.Summary = fmt.Sprintf("Agent %q was stopped", label)
	}
	return notice
}

// wakeForNotices runs a notice-only turn on sessionID. It is called from
// inside a finishing turn, so the turn runs on its own goroutine and on the
// coordinator's lifetime rather than on the context of whichever turn
// happened to notice.
func (c *coordinator) wakeForNotices(sessionID string) {
	if c.lifetime == nil {
		return
	}
	go func() {
		_, err := c.run(withNoticeWake(c.lifetime), nil, sessionID, "")
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("Failed to deliver background agent notices", "session", sessionID, "error", err)
		}
	}()
}

// classifyBackgroundOutcome maps how a sub-agent run ended onto the status
// its notice carries. A run that produced an answer is completed even if a
// stop landed at the same moment, because the answer is what the parent
// wants. Cancellation is read from the run's own error as well as from
// ctx, since the sub-session can be cancelled from the UI without the
// agent's context ever being touched.
func classifyBackgroundOutcome(ctx context.Context, outcome subAgentOutcome) tools.BackgroundAgentStatus {
	switch {
	case outcome.Failure == "":
		return tools.BackgroundAgentCompleted
	case ctx.Err() != nil || errors.Is(outcome.Err, context.Canceled):
		return tools.BackgroundAgentStopped
	default:
		return tools.BackgroundAgentFailed
	}
}
