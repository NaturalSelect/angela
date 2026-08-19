package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestTurnKeepsItsModelWhenConfigChangesMidRun pins the invariant that
// makes per-session agents possible at all: once a turn has resolved
// the agent it runs on, nothing that happens afterwards may change the
// model that turn is using.
//
// Before resolution became a value, the coordinator held a single
// shared SessionAgent and UpdateModels rewrote its model in place with
// whatever config.AgentCoder resolved to. A config edit — or simply a
// second session starting — therefore swapped the model underneath a
// turn already streaming. The gate below reproduces exactly that
// window: the run is parked inside Stream while the config changes.
func TestTurnKeepsItsModelWhenConfigChangesMidRun(t *testing.T) {
	env := testEnv(t)
	coord := newModelPrefTestCoordinator(t, nil)

	gated := &gatedStreamModel{
		text:    "done",
		gate:    make(chan struct{}),
		entered: make(chan struct{}),
	}
	resolved := resolveCoder(t, coord)
	require.Equal(t, "small-model", resolved.Model.ModelCfg.Model,
		"sanity check: this turn resolved the chore model")
	// Swap in the gated stream so the turn parks mid-flight, keeping
	// the rest of the resolved value (the chore model config) intact.
	resolved.Model.Model = gated

	sa := NewSessionAgent(SessionAgentOptions{
		IsYolo:   true,
		Sessions: env.sessions,
		Messages: env.messages,
	})

	sess, err := env.sessions.Create(t.Context(), "session")
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = sa.Run(t.Context(), SessionAgentCall{
			SessionID: sess.ID,
			Agent:     resolved,
			Prompt:    "hello",
		})
	}()

	// The turn is now parked inside Stream. Move the coder onto the
	// main model and refresh the coordinator, the way a live config
	// reload would.
	<-gated.entered
	mainCfg := coord.cfg.Config().Agents[config.AgentCoder]
	mainCfg.Model = config.ModelMain
	coord.cfg.Config().Agents[config.AgentCoder] = mainCfg
	require.NoError(t, coord.UpdateModels(context.Background()))

	close(gated.gate)
	<-done

	msgs, err := env.messages.List(t.Context(), sess.ID)
	require.NoError(t, err)
	require.NotEmpty(t, msgs)

	var assistantModel string
	for _, m := range msgs {
		if m.Role == "assistant" {
			assistantModel = m.Model
		}
	}
	require.Equal(t, "small-model", assistantModel,
		"the turn recorded a model it never ran on: resolution leaked between turns")

	// The next turn does pick up the change — resolution is per turn,
	// not frozen for the life of the coordinator.
	require.Equal(t, "large-model", resolveCoder(t, coord).Model.ModelCfg.Model)
}

// TestConcurrentResolutionIsRacefree runs two resolutions against the
// same coordinator at once. Under -race this catches an unsynchronized
// hand-off that in-place mutation of a shared agent would create.
func TestConcurrentResolutionIsRacefree(t *testing.T) {
	coord := newModelPrefTestCoordinator(t, nil)

	choreActive := instantiate(t, coord, config.AgentCoder)
	mainActive := choreActive
	mainActive.ModelName = config.ModelMain
	mainActive.Model = coord.cfg.Config().Models[config.ModelMain]

	var wg sync.WaitGroup
	models := make([]string, 2)
	for i, active := range []config.ActiveAgent{choreActive, mainActive} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := coord.buildModel(context.Background(), active, false)
			if err != nil {
				t.Error(err)
				return
			}
			models[i] = m.ModelCfg.Model
		}()
	}
	wg.Wait()

	require.Equal(t, "small-model", models[0])
	require.Equal(t, "large-model", models[1])
}
