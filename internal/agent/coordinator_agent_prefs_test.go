package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/stretchr/testify/require"
)

// newModelPrefTestCoordinator builds a coordinator like
// newGateTestCoordinator, but with distinct main/chore model IDs so
// tests can tell which one ended up as the coder agent's model. The
// coder agent is configured to prefer the chore model and carries the
// given temperature override.
func newModelPrefTestCoordinator(t *testing.T, temperature *float64) *coordinator {
	t.Helper()

	env := testEnv(t)

	angelaJSON := `{
  "options": {"disable_default_providers": true, "disable_provider_auto_update": true},
  "providers": {"mock": {"id": "mock", "name": "Mock", "type": "openai",
    "base_url": "http://127.0.0.1:9/v1", "api_key": "test-key",
    "models": [{"id": "large-model", "name": "Large", "context_window": 8192, "max_tokens": 128},
               {"id": "small-model", "name": "Small", "context_window": 8192, "max_tokens": 128}]}},
  "slots": {"main": {"provider": "mock", "model": "large-model"},
             "chore": {"provider": "mock", "model": "small-model"}}
}`
	require.NoError(t, os.WriteFile(filepath.Join(env.workingDir, "angela.json"), []byte(angelaJSON), 0o644))

	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	cfg.SetupAgents()

	agentCfg := cfg.Config().Agents[config.AgentCoder]
	agentCfg.Slot = config.SlotChore
	agentCfg.Temperature = temperature
	cfg.Config().Agents[config.AgentCoder] = agentCfg

	coord := &coordinator{
		cfg:            cfg,
		sessions:       env.sessions,
		messages:       env.messages,
		permissions:    env.permissions,
		history:        env.history,
		filetracker:    *env.filetracker,
		subagents:      newSubagentRegistry(),
		branches:       newBranchController(),
		subagentRoutes: csync.NewMap[string, subagentRoute](),
	}
	coord.reconcileSubagents()
	coord.rootExecutor = coord.buildAgent(agentCfg.ID, false)

	return coord
}

// resolveCoder resolves the coder agent the way a turn does.
func resolveCoder(t *testing.T, coord *coordinator) resolvedAgent {
	t.Helper()
	resolved, err := coord.resolveAgent(context.Background(), instantiate(t, coord, config.AgentCoder), 0)
	require.NoError(t, err)
	return resolved
}

// instantiate builds an agent's session instance straight from config,
// which is what a session's first turn does.
func instantiate(t *testing.T, coord *coordinator, agentID string) config.ActiveAgent {
	t.Helper()
	active, ok := coord.cfg.Config().InstantiateAgent(agentID)
	require.True(t, ok, "agent %q must be configured", agentID)
	return active
}

// TestBuildModelResolvesConfiguredName pins that an agent gets the one
// model its Model name points at, and that an unset name falls back to
// main. There is no second model to cross over to any more: whatever an
// agent names is what it runs on.
func TestBuildModelResolvesConfiguredName(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	// The coder is configured to prefer the chore model.
	chore, err := coord.models().buildModel(context.Background(), instantiate(t, coord, config.AgentCoder), false)
	require.NoError(t, err)
	require.Equal(t, "small-model", chore.ModelCfg.Model)

	// An agent that names no model at all falls back to main.
	coord.cfg.Config().Agents["unnamed"] = config.Agent{ID: "unnamed"}
	main, err := coord.models().buildModel(context.Background(), instantiate(t, coord, "unnamed"), false)
	require.NoError(t, err)
	require.Equal(t, "large-model", main.ModelCfg.Model)
}

// TestBuildAgentWiresTitleGenerator pins that buildAgent hands the
// session agent a title generator. Nothing else fails when that wiring
// is dropped — sessions just silently stop getting names.
func TestBuildAgentWiresTitleGenerator(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	agent, ok := coord.rootExecutor.(*sessionAgent)
	require.True(t, ok)

	require.NotNil(t, agent.generateTitle,
		"buildAgent must wire a title generator onto the session agent")
}

// TestUpdateModelsRespectsAgentModelPreference reproduces H4's exact
// failure scenario: an agent configured with Model: "chore" must keep
// resolving the chore model turn after turn, not silently fall back to
// main once the coordinator refreshes.
func TestUpdateModelsRespectsAgentModelPreference(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	require.Equal(t, "small-model", resolveCoder(t, coord).Model.ModelCfg.Model,
		"sanity check: the first resolution must select the chore model")

	require.NoError(t, coord.UpdateModels(context.Background()))

	require.Equal(t, "small-model", resolveCoder(t, coord).Model.ModelCfg.Model,
		"a later turn must keep the agent's chore-model preference, not reset it to main")
}

// TestBuildAgentAppliesTemperature pins the M2 fix end to end: an
// agent's configured Temperature must show up on the resulting
// SessionAgent's model, both right after construction and after
// UpdateModels refreshes the models.
func TestBuildAgentAppliesTemperature(t *testing.T) {
	want := 0.1
	coord := newModelPrefTestCoordinator(t, &want)

	first := resolveCoder(t, coord)
	require.NotNil(t, first.Model.CatwalkCfg.Options.Temperature)
	require.Equal(t, want, *first.Model.CatwalkCfg.Options.Temperature)

	require.NoError(t, coord.UpdateModels(context.Background()))

	later := resolveCoder(t, coord)
	require.NotNil(t, later.Model.CatwalkCfg.Options.Temperature)
	require.Equal(t, want, *later.Model.CatwalkCfg.Options.Temperature,
		"every turn must keep applying the agent's Temperature override")
}

// pickModelFor sends the edit "Switch Agent Model" sends: a model for
// agentID, recorded in the session's pick table.
func pickModelFor(t *testing.T, coord *coordinator, sessionID, agentID, model string) {
	t.Helper()
	_, err := coord.EditActiveAgent(t.Context(), sessionID, config.ActiveAgentEdit{
		ForAgent: agentID,
		Model:    &config.SelectedModel{Provider: "mock", Model: model},
	})
	require.NoError(t, err)
}

// TestAgentModelPickOutranksAgentConfig pins that a "switch agent
// model" pick reaches buildModel, and still does after the session's
// instance is dropped and rebuilt from its record, which is what a
// restart does.
func TestAgentModelPickOutranksAgentConfig(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	sessionID := newVariantSession(t, coord)

	// The coder is configured to prefer the chore (small) model.
	pickModelFor(t, coord, sessionID, config.AgentCoder, "large-model")

	for _, stage := range []string{"live", "restored"} {
		if stage == "restored" {
			coord.active.forget(sessionID)
		}
		active, err := coord.activeAgentFor(t.Context(), sessionID)
		require.NoError(t, err)
		model, err := coord.models().buildModel(context.Background(), active, false)
		require.NoError(t, err)
		require.Equal(t, "large-model", model.ModelCfg.Model,
			"the pick must outrank the agent's own configured slot (%s)", stage)
	}
}

// TestAgentModelPickReachesSubagentDispatch pins that a pick made for
// a sub-agent applies when the agent tool dispatches it under that
// session, not just to the session's own primary agent: resolveSubagent
// rebuilds the parent from its record and resolves the child under it.
func TestAgentModelPickReachesSubagentDispatch(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	childID := persistedChildSession(t, coord, config.AgentExplore)
	child, err := coord.sessions.Get(t.Context(), childID)
	require.NoError(t, err)

	pickModelFor(t, coord, child.ParentSessionID, config.AgentExplore, "large-model")

	resolved, err := coord.resolveSubagent(context.Background(), config.AgentExplore, childID)
	require.NoError(t, err)
	require.Equal(t, "large-model", resolved.Model.ModelCfg.Model,
		"a sub-agent dispatch must see the pick its session made for it")
}

// TestPickMadeInABranchReachesItsNextTurn pins that a model pick made
// while viewing a branch changes what that branch's next turn runs on
// and not only what the UI shows.
func TestPickMadeInABranchReachesItsNextTurn(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	childID := persistedChildSession(t, coord, config.AgentExplore)
	asBranch(t, coord, childID)

	before, err := coord.turnExecutorFor(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, "large-model", before.resolved.Model.ModelCfg.Model,
		"test premise: explore is configured on the main model")

	_, err = coord.EditActiveAgent(t.Context(), childID, switchModelEdit("small-model"))
	require.NoError(t, err)

	after, err := coord.turnExecutorFor(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, "small-model", after.resolved.Model.ModelCfg.Model,
		"a pick made in the branch must change the branch's next turn")
}

// TestASubAgentCannotEditItsOwnModel pins that a child which is not a
// live branch runs on what its tree gives it. Every edit aimed at the
// session's own state is refused and writes nothing.
func TestASubAgentCannotEditItsOwnModel(t *testing.T) {
	for name, edit := range map[string]config.ActiveAgentEdit{
		"switch model":    switchModelEdit("small-model"),
		"select preset":   {Variant: ptrTo("deep")},
		"toggle thinking": {ToggleThink: true},
		"set thinking":    {Think: ptrTo(true)},
		"reset own model": {ClearSessionModel: true},
	} {
		t.Run(name, func(t *testing.T) {
			coord := newModelPrefTestCoordinator(t, nil)
			_, childID := exploreChild(t, coord)
			before, err := coord.sessions.Get(t.Context(), childID)
			require.NoError(t, err)

			_, err = coord.EditActiveAgent(t.Context(), childID, edit)
			require.ErrorIs(t, err, ErrSubAgentModelLocked)

			after, err := coord.sessions.Get(t.Context(), childID)
			require.NoError(t, err)
			require.Equal(t, before.ActiveAgent, after.ActiveAgent,
				"a refused edit must not touch the child's row")
		})
	}
}

// TestABranchThatEndedIsLockedLikeASubAgent pins that the right to edit
// a model belongs to a branch only while its parent waits on it. Once
// it is merged or abandoned it is a read-only child again.
func TestABranchThatEndedIsLockedLikeASubAgent(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	_, childID := exploreChild(t, coord)
	asBranch(t, coord, childID)

	_, err := coord.EditActiveAgent(t.Context(), childID, switchModelEdit("small-model"))
	require.NoError(t, err)

	coord.branches.Forget(childID)

	_, err = coord.EditActiveAgent(t.Context(), childID, switchModelEdit("large-model"))
	require.ErrorIs(t, err, ErrSubAgentModelLocked)
}

// exploreChild creates an explore child session and returns it together
// with the session that dispatched it.
func exploreChild(t *testing.T, coord *coordinator) (parentID, childID string) {
	t.Helper()
	childID = persistedChildSession(t, coord, config.AgentExplore)
	child, err := coord.sessions.Get(t.Context(), childID)
	require.NoError(t, err)
	return child.ParentSessionID, childID
}

// asBranch makes childID a branch whose parent is waiting on it, which
// is what entitles a child session to a model of its own.
func asBranch(t *testing.T, coord *coordinator, childID string) {
	t.Helper()
	child, err := coord.sessions.Get(t.Context(), childID)
	require.NoError(t, err)
	coord.branches.Register(childID, child.ParentSessionID)
}

// siblingChild creates a second explore child under parentID.
func siblingChild(t *testing.T, coord *coordinator, parentID string) string {
	t.Helper()
	id := coord.sessions.CreateAgentToolSessionID("msg-2", "call-2")
	_, err := coord.sessions.CreateTaskSession(t.Context(), id, parentID, "second explore run")
	require.NoError(t, err)
	require.NoError(t, coord.sessions.UpdateActiveAgent(t.Context(), id,
		config.ActiveAgentState{Agent: config.AgentExplore}))
	return id
}

// TestBranchModelOutranksTheTreePick pins the precedence between the
// two layers a branch resolves from: the branch's own model wins over
// the pick the tree made for the same agent, and the branch's row holds
// that model alone rather than a copy of the tree's table.
func TestBranchModelOutranksTheTreePick(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	parentID, childID := exploreChild(t, coord)
	asBranch(t, coord, childID)

	pickModelFor(t, coord, parentID, config.AgentExplore, "small-model")
	pickModelFor(t, coord, parentID, config.AgentGeneral, "small-model")

	_, err := coord.EditActiveAgent(t.Context(), childID, switchModelEdit("large-model"))
	require.NoError(t, err)

	resolved, err := coord.resolveSubagent(t.Context(), config.AgentExplore, childID)
	require.NoError(t, err)
	require.Equal(t, "large-model", resolved.Model.ModelCfg.Model,
		"the branch's own model must outrank the tree's pick for the same agent")

	stored, err := coord.sessions.Get(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, config.AgentModelPick{Model: config.SelectedModel{Provider: "mock", Model: "large-model"}},
		stored.ActiveAgent.SessionModel,
		"the branch's row must hold only what was picked in the branch")
	require.Empty(t, stored.ActiveAgent.AgentPicks,
		"the tree's table lives on the root and never on a child's row")
}

// TestChildShowsTheModelItRuns pins that the instance a child session
// reports is the one its next turn runs as, picks made by its dispatcher
// included.
func TestChildShowsTheModelItRuns(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	parentID, childID := exploreChild(t, coord)
	pickModelFor(t, coord, parentID, config.AgentExplore, "small-model")

	active, model, err := coord.ActiveAgent(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, "small-model", model.ModelCfg.Model,
		"the header must show the model the next turn runs on")
	require.Contains(t, active.AgentPicks, config.AgentExplore,
		"the dialogs highlight against the table that applies to the child")

	turn, err := coord.turnExecutorFor(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, model.ModelCfg.Model, turn.resolved.Model.ModelCfg.Model)
}

// TestPickingTheModelABranchAlreadyRunsRecordsNothing pins that an edit
// in a branch is judged against what the branch runs, not against what
// its own row alone would resolve to.
func TestPickingTheModelABranchAlreadyRunsRecordsNothing(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	parentID, childID := exploreChild(t, coord)
	asBranch(t, coord, childID)
	pickModelFor(t, coord, parentID, config.AgentExplore, "small-model")

	_, err := coord.EditActiveAgent(t.Context(), childID, switchModelEdit("small-model"))
	require.NoError(t, err)

	stored, err := coord.sessions.Get(t.Context(), childID)
	require.NoError(t, err)
	require.True(t, stored.ActiveAgent.SessionModel.IsZero(),
		"the branch already runs this model, so there is nothing to record")
}

// TestPresetOnlyPickInABranchKeepsTheTreesModel pins that choosing a
// preset in a branch does not move the branch off the model its tree
// picked for it.
func TestPresetOnlyPickInABranchKeepsTheTreesModel(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	setChoreVariants(t, coord, map[string]config.SelectedModelOverride{
		"deep": {MaxTokens: ptrTo(int64(32000))},
	})
	parentID, childID := exploreChild(t, coord)
	asBranch(t, coord, childID)
	pickModelFor(t, coord, parentID, config.AgentExplore, "small-model")

	require.NoError(t, coord.SwitchVariant(t.Context(), childID, "deep"))

	turn, err := coord.turnExecutorFor(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, "small-model", turn.resolved.Model.ModelCfg.Model,
		"a preset-only pick must leave the model to the tree's pick")
	require.Equal(t, int64(32000), turn.resolved.Model.CatwalkCfg.DefaultMaxTokens,
		"the preset must reach the model it was picked on")
}

// TestToggleThinkInABranchStartsFromWhatItRuns pins that a relative
// thinking edit in a branch flips the value the branch actually runs
// with, which an agent on the inherited slot takes from its dispatcher.
func TestToggleThinkInABranchStartsFromWhatItRuns(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	inheriting := coord.cfg.Config().Agents[config.AgentExplore]
	inheriting.Slot = config.SlotInherited
	coord.cfg.Config().Agents[config.AgentExplore] = inheriting
	parentID, childID := exploreChild(t, coord)
	asBranch(t, coord, childID)

	_, err := coord.EditActiveAgent(t.Context(), parentID, config.ActiveAgentEdit{Think: ptrTo(true)})
	require.NoError(t, err)

	active, err := coord.EditActiveAgent(t.Context(), childID, config.ActiveAgentEdit{ToggleThink: true})
	require.NoError(t, err)
	require.False(t, active.Think, "the branch runs with thinking on, so a toggle turns it off")

	turn, err := coord.turnExecutorFor(t.Context(), childID)
	require.NoError(t, err)
	require.False(t, turn.resolved.Model.Think)
}

// TestResetSessionModelInABranchClearsOnlyItsOwnModel pins what the
// session reset reaches: the branch's own model. The pick the tree made
// for the agent is the root's to reset.
func TestResetSessionModelInABranchClearsOnlyItsOwnModel(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	parentID, childID := exploreChild(t, coord)
	asBranch(t, coord, childID)
	pickModelFor(t, coord, parentID, config.AgentExplore, "small-model")

	_, err := coord.EditActiveAgent(t.Context(), childID, config.ActiveAgentEdit{ClearSessionModel: true})
	require.NoError(t, err, "a reset with nothing to clear is a no-op, not an error")

	_, err = coord.EditActiveAgent(t.Context(), childID, switchModelEdit("large-model"))
	require.NoError(t, err)
	_, err = coord.EditActiveAgent(t.Context(), childID, config.ActiveAgentEdit{ClearSessionModel: true})
	require.NoError(t, err)

	stored, err := coord.sessions.Get(t.Context(), childID)
	require.NoError(t, err)
	require.True(t, stored.ActiveAgent.SessionModel.IsZero())

	active, model, err := coord.ActiveAgent(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, "small-model", model.ModelCfg.Model,
		"the tree's pick still applies once the branch's own model is gone")
	require.Contains(t, active.AgentPicks, config.AgentExplore)

	parent, err := coord.sessions.Get(t.Context(), parentID)
	require.NoError(t, err)
	require.Contains(t, parent.ActiveAgent.AgentPicks, config.AgentExplore,
		"a session reset must not reach the tree's picks")
}

// TestResetAgentPicksInAChildClearsTheRoot pins that the tree-wide reset
// is the same wherever it is issued: it empties the root's table, even
// from a sub-agent that may not edit a model of its own.
func TestResetAgentPicksInAChildClearsTheRoot(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	parentID, childID := exploreChild(t, coord)
	pickModelFor(t, coord, parentID, config.AgentExplore, "small-model")

	_, err := coord.EditActiveAgent(t.Context(), childID, config.ActiveAgentEdit{ClearAgentPicks: true})
	require.NoError(t, err)

	parent, err := coord.sessions.Get(t.Context(), parentID)
	require.NoError(t, err)
	require.Empty(t, parent.ActiveAgent.AgentPicks)

	_, model, err := coord.ActiveAgent(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, "large-model", model.ModelCfg.Model,
		"with the tree's pick gone the child follows the config again")
}

// TestAChildCannotSwitchAgent pins that the agent a child runs is the
// one dispatched into it. Its route binds it to that agent, so a switch
// recorded on its row could not take effect in this process and would
// take effect after a restart.
func TestAChildCannotSwitchAgent(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	_, childID := exploreChild(t, coord)

	_, err := coord.EditActiveAgent(t.Context(), childID, config.ActiveAgentEdit{Agent: config.AgentCoder})
	require.ErrorIs(t, err, ErrAgentNotAvailable)

	stored, err := coord.sessions.Get(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, config.AgentExplore, stored.Agent)
}

// TestSwitchAgentModelFromAChildLandsOnTheRoot pins where a tree-wide
// pick is written: on the root session however deep the edit was made,
// never on the child's own row, and every other session in the tree
// reads it on its next turn.
func TestSwitchAgentModelFromAChildLandsOnTheRoot(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	parentID, childID := exploreChild(t, coord)
	siblingID := siblingChild(t, coord, parentID)
	before, err := coord.sessions.Get(t.Context(), childID)
	require.NoError(t, err)

	pickModelFor(t, coord, childID, config.AgentExplore, "small-model")

	root, err := coord.sessions.Get(t.Context(), parentID)
	require.NoError(t, err)
	require.Contains(t, root.ActiveAgent.AgentPicks, config.AgentExplore,
		"the pick must land on the root session")

	after, err := coord.sessions.Get(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, before.ActiveAgent, after.ActiveAgent,
		"the child's own row must not change")

	for name, id := range map[string]string{"the editing child": childID, "its sibling": siblingID} {
		turn, err := coord.turnExecutorFor(t.Context(), id)
		require.NoError(t, err)
		require.Equal(t, "small-model", turn.resolved.Model.ModelCfg.Model,
			"%s must run the tree's pick on its next turn", name)
	}
}

// TestSwitchModelDoesNotReachNonInheritedChildren pins that a session's
// own model is its own: an agent dispatched from it that names a slot of
// its own keeps following the config.
func TestSwitchModelDoesNotReachNonInheritedChildren(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	cfgExplore := coord.cfg.Config().Agents[config.AgentExplore]
	cfgExplore.Slot = config.SlotChore
	coord.cfg.Config().Agents[config.AgentExplore] = cfgExplore
	parentID, childID := exploreChild(t, coord)

	_, err := coord.EditActiveAgent(t.Context(), parentID, switchModelEdit("large-model"))
	require.NoError(t, err)

	turn, err := coord.turnExecutorFor(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, "small-model", turn.resolved.Model.ModelCfg.Model,
		"an agent on its own slot must not follow its dispatcher's model")
}

// TestSwitchModelReachesInheritedChildrenLive pins that an agent on the
// inherited slot follows what its dispatcher runs now, with no
// snapshot taken when it was dispatched.
func TestSwitchModelReachesInheritedChildrenLive(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	inheriting := coord.cfg.Config().Agents[config.AgentExplore]
	inheriting.Slot = config.SlotInherited
	coord.cfg.Config().Agents[config.AgentExplore] = inheriting
	parentID, childID := exploreChild(t, coord)

	turn, err := coord.turnExecutorFor(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, "small-model", turn.resolved.Model.ModelCfg.Model,
		"test premise: the child follows the coder's configured model")

	_, err = coord.EditActiveAgent(t.Context(), parentID, switchModelEdit("large-model"))
	require.NoError(t, err)

	turn, err = coord.turnExecutorFor(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, "large-model", turn.resolved.Model.ModelCfg.Model,
		"the child must follow its dispatcher's new model on its next turn")
}

// TestTreePickOutranksInheritingTheDispatcher pins the order of the two
// lower layers for an agent on the inherited slot: a pick made for it
// wins over the model it would have borrowed.
func TestTreePickOutranksInheritingTheDispatcher(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	inheriting := coord.cfg.Config().Agents[config.AgentExplore]
	inheriting.Slot = config.SlotInherited
	coord.cfg.Config().Agents[config.AgentExplore] = inheriting
	parentID, childID := exploreChild(t, coord)

	pickModelFor(t, coord, parentID, config.AgentExplore, "large-model")
	_, err := coord.EditActiveAgent(t.Context(), parentID, switchModelEdit("small-model"))
	require.NoError(t, err)

	turn, err := coord.turnExecutorFor(t.Context(), childID)
	require.NoError(t, err)
	require.Equal(t, "large-model", turn.resolved.Model.ModelCfg.Model)
}

// TestSwitchAgentKeepsTheSessionModel pins that the session's own model
// belongs to the session and not to the agent it happened to run when
// the model was picked.
func TestSwitchAgentKeepsTheSessionModel(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	addReviewerAgent(t, coord)
	reviewer := coord.cfg.Config().Agents[testReviewerAgent]
	reviewer.Slot = config.SlotChore
	coord.cfg.Config().Agents[testReviewerAgent] = reviewer
	sessionID := newVariantSession(t, coord)

	_, err := coord.EditActiveAgent(t.Context(), sessionID, switchModelEdit("large-model"))
	require.NoError(t, err)
	require.NoError(t, coord.SwitchAgent(t.Context(), sessionID, testReviewerAgent))

	stored, err := coord.sessions.Get(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, testReviewerAgent, stored.Agent)
	require.Equal(t, "large-model", stored.ActiveAgent.SessionModel.Model.Model,
		"switching agent must not clear the session's model")

	active, err := coord.activeAgentFor(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, "large-model", active.Model.Model,
		"the reviewer is configured on the chore model, so only the session's pick can put it on the large one")
}

// TestMixedActiveAgentEditIsRejected pins that one edit never changes
// both the tree and the session: the two take different locks and land
// on different rows, so there is no single outcome to report.
func TestMixedActiveAgentEditIsRejected(t *testing.T) {
	large := &config.SelectedModel{Provider: "mock", Model: "large-model"}
	for name, edit := range map[string]config.ActiveAgentEdit{
		"tree pick with thinking":        {ForAgent: config.AgentExplore, Model: large, Think: ptrTo(true)},
		"tree pick with an agent":        {ForAgent: config.AgentExplore, Model: large, Agent: config.AgentCoder},
		"both resets":                    {ClearAgentPicks: true, ClearSessionModel: true},
		"tree reset with a session pick": {ClearAgentPicks: true, Model: large},
	} {
		t.Run(name, func(t *testing.T) {
			coord := newModelPrefTestCoordinator(t, nil)
			sessionID := newVariantSession(t, coord)
			before, err := coord.sessions.Get(t.Context(), sessionID)
			require.NoError(t, err)

			_, err = coord.EditActiveAgent(t.Context(), sessionID, edit)
			require.ErrorIs(t, err, ErrMixedActiveAgentEdit)

			after, err := coord.sessions.Get(t.Context(), sessionID)
			require.NoError(t, err)
			require.Equal(t, before.ActiveAgent, after.ActiveAgent, "a rejected edit changes nothing")
		})
	}
}

// TestDispatchedChildRecordsOnlyItsAgent pins what a dispatch writes on
// the child's row. The child resolves everything else live, so nothing
// the host held at that moment, thinking included, may be frozen there.
func TestDispatchedChildRecordsOnlyItsAgent(t *testing.T) {
	t.Parallel()

	host := config.ActiveAgent{
		Agent:        config.Agent{ID: config.AgentCoder},
		SessionModel: config.AgentModelPick{Model: config.SelectedModel{Provider: "mock", Model: "large-model"}},
		AgentPicks: map[string]config.AgentModelPick{
			config.AgentExplore: {Model: config.SelectedModel{Provider: "mock", Model: "small-model"}},
		},
		Think:     true,
		ThinkPick: ptrTo(true),
	}

	require.Equal(t, config.ActiveAgentState{Agent: config.AgentCoder}, dispatchedState(host))
}

// TestAgentModelPickStaysInItsSessionTree pins the scope of a pick: it
// lives on one tree's root, so another top-level session keeps
// resolving the agent's configured model.
func TestAgentModelPickStaysInItsSessionTree(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	picked := newVariantSession(t, coord)
	other := newVariantSession(t, coord)

	pickModelFor(t, coord, picked, config.AgentCoder, "large-model")

	otherAgent, err := coord.activeAgentFor(t.Context(), other)
	require.NoError(t, err)
	require.Equal(t, "small-model", otherAgent.Model.Model,
		"a pick made in one tree must not reach another")
	require.Empty(t, otherAgent.AgentPicks)
}

// TestSessionModelPickSurvivesAColdCache pins that the session record
// alone is enough to rebuild the pick. materializeActiveAgent is what a
// fresh load, or a new coordinator after a restart, goes through.
func TestSessionModelPickSurvivesAColdCache(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)
	sessionID := newVariantSession(t, coord)

	// The session explicitly moves off the agent's configured default
	// (chore/small-model) onto the other model.
	_, err := coord.EditActiveAgent(t.Context(), sessionID, switchModelEdit("large-model"))
	require.NoError(t, err)

	sess, err := coord.sessions.Get(t.Context(), sessionID)
	require.NoError(t, err)
	require.Equal(t, "large-model", sess.ActiveAgent.SessionModel.Model.Model,
		"switching the model lands in the session's own layer")
	require.Empty(t, sess.ActiveAgent.AgentPicks,
		"and not in the tree's table")

	agentCfg, err := coord.materializeActiveAgent(sessionID, sess.ActiveAgent)
	require.NoError(t, err)
	require.Equal(t, "large-model", agentCfg.Model.Model)
}
