package backend

import (
	"context"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
)

// fakeCoordinator is a fully configurable agent.Coordinator stub for
// tests that need to observe delegation through distinguishable return
// values, unlike blockingCoordinator/errorCoordinator (defined
// elsewhere in this package) which always return zero values.
type fakeCoordinator struct {
	model agent.Model
	busy  bool

	sessionBusy   map[string]bool
	sessionBranch map[string]bool
	queued        map[string]int
	queuedList    map[string][]string

	clearedQueue []string
	abandoned    []string
	cancelled    []string

	editEdit   config.ActiveAgentEdit
	editResult config.ActiveAgent
	editErr    error

	activeResult config.ActiveAgent
	activeModel  agent.Model
	activeErr    error

	summarizeErr   error
	summarizeCalls []string
}

func (c *fakeCoordinator) Run(context.Context, string, string, ...message.Attachment) (*fantasy.AgentResult, error) {
	return nil, nil
}

func (c *fakeCoordinator) RunAccepted(context.Context, *agent.AcceptedRun, string, string, ...message.Attachment) (*fantasy.AgentResult, error) {
	return nil, nil
}

func (c *fakeCoordinator) BeginAccepted(context.Context, string) *agent.AcceptedRun { return nil }
func (c *fakeCoordinator) LockSession(context.Context, string) (func(), bool)       { return nil, false }

func (c *fakeCoordinator) Cancel(sessionID string) {
	c.cancelled = append(c.cancelled, sessionID)
}

func (c *fakeCoordinator) AbandonBranch(sessionID string) bool {
	c.abandoned = append(c.abandoned, sessionID)
	return true
}

func (c *fakeCoordinator) CancelAll()   {}
func (c *fakeCoordinator) IsBusy() bool { return c.busy }

func (c *fakeCoordinator) IsSessionBusy(sessionID string) bool { return c.sessionBusy[sessionID] }

func (c *fakeCoordinator) IsSessionBranch(sessionID string) bool { return c.sessionBranch[sessionID] }

func (c *fakeCoordinator) QueuedPrompts(sessionID string) int { return c.queued[sessionID] }

func (c *fakeCoordinator) QueuedPromptsList(sessionID string) []string {
	return c.queuedList[sessionID]
}

func (c *fakeCoordinator) ClearQueue(sessionID string) {
	c.clearedQueue = append(c.clearedQueue, sessionID)
}

func (c *fakeCoordinator) Summarize(ctx context.Context, sessionID string) error {
	c.summarizeCalls = append(c.summarizeCalls, sessionID)
	return c.summarizeErr
}

func (c *fakeCoordinator) DefaultModel() agent.Model { return c.model }

func (c *fakeCoordinator) EditActiveAgent(ctx context.Context, sessionID string, edit config.ActiveAgentEdit) (config.ActiveAgent, error) {
	c.editEdit = edit
	return c.editResult, c.editErr
}

func (c *fakeCoordinator) ActiveAgent(context.Context, string) (config.ActiveAgent, agent.Model, error) {
	return c.activeResult, c.activeModel, c.activeErr
}

func (c *fakeCoordinator) UpdateModels(context.Context) error            { return nil }
func (c *fakeCoordinator) GenerateTitle(context.Context, string, string) {}

func (c *fakeCoordinator) GenerateAgent(context.Context, string) (config.Agent, string, error) {
	return config.Agent{}, "", nil
}

func (c *fakeCoordinator) SwitchAgent(context.Context, string, string) error   { return nil }
func (c *fakeCoordinator) SwitchVariant(context.Context, string, string) error { return nil }
