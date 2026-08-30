package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/azure"
	"charm.land/fantasy/providers/bedrock"
	"charm.land/fantasy/providers/openai"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockSessionAgent is a minimal mock for the SessionAgent interface.
type mockSessionAgent struct {
	model     Model
	agentID   string
	runFunc   func(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error)
	cancelled []string
	cleared   []string
	cancelAll int
	busy      bool
	queued    []string
}

func (m *mockSessionAgent) Run(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
	return m.runFunc(ctx, call)
}

func (m *mockSessionAgent) BeginAccepted(sessionID string) *AcceptedRun {
	return &AcceptedRun{sessionID: sessionID}
}

func (m *mockSessionAgent) AgentID() string { return m.agentID }
func (m *mockSessionAgent) Cancel(sessionID string) {
	m.cancelled = append(m.cancelled, sessionID)
}
func (m *mockSessionAgent) CancelAll()                                  { m.cancelAll++ }
func (m *mockSessionAgent) IsSessionBusy(sessionID string) bool         { return m.busy }
func (m *mockSessionAgent) IsBusy() bool                                { return m.busy }
func (m *mockSessionAgent) QueuedPrompts(sessionID string) int          { return len(m.queued) }
func (m *mockSessionAgent) QueuedPromptsList(sessionID string) []string { return m.queued }
func (m *mockSessionAgent) ClearQueue(sessionID string) {
	m.cleared = append(m.cleared, sessionID)
}

func (m *mockSessionAgent) Summarize(context.Context, string, CompactAgent, fantasy.ProviderOptions, func(context.Context, *fantasy.ProviderError) error) error {
	return nil
}

// newTestCoordinator creates a minimal coordinator for unit testing runSubAgent.
func newTestCoordinator(t *testing.T, env fakeEnv, providerID string, providerCfg config.ProviderConfig) *coordinator {
	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	cfg.Config().Providers.Set(providerID, providerCfg)
	return &coordinator{
		cfg:            cfg,
		sessions:       env.sessions,
		messages:       env.messages,
		permissions:    env.permissions,
		subagents:      newSubagentRegistry(),
		branches:       newBranchController(),
		proposals:      tools.NewProposalStore(),
		subagentRoutes: csync.NewMap[string, subagentRoute](),
	}
}

// newMockAgent creates a mockSessionAgent plus the resolution a
// dispatch would hand it, since model and token budget now travel on
// the call rather than living on the agent.
func newMockAgent(providerID string, maxTokens int64, runFunc func(context.Context, SessionAgentCall) (*fantasy.AgentResult, error)) (*mockSessionAgent, resolvedAgent) {
	model := Model{
		CatwalkCfg: catwalk.Model{
			DefaultMaxTokens: maxTokens,
		},
		ModelCfg: config.SelectedModel{
			Provider: providerID,
		},
	}
	return &mockSessionAgent{runFunc: runFunc},
		resolvedAgent{Model: model, MaxTokens: maxTokensFor(config.Agent{}, model)}
}

// agentResultWithText creates a minimal AgentResult with the given text response.
func agentResultWithText(text string) *fantasy.AgentResult {
	return &fantasy.AgentResult{
		Response: fantasy.Response{
			Content: fantasy.ResponseContent{
				fantasy.TextContent{Text: text},
			},
		},
	}
}

func TestRunSubAgent(t *testing.T) {
	const providerID = "test-provider"
	providerCfg := config.ProviderConfig{ID: providerID}

	t.Run("happy path", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent, resolved := newMockAgent(providerID, 4096, func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
			assert.Equal(t, "do something", call.Prompt)
			assert.Equal(t, int64(4096), call.MaxOutputTokens)
			return agentResultWithText("done"), nil
		})

		resp, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			Resolved:       resolved,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "do something",
			SessionTitle:   "Test Session",
		})
		require.NoError(t, err)
		assert.Equal(t, "done", resp.Content)
		assert.False(t, resp.IsError)
	})

	t.Run("cost update failure preserves output", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		agent, resolved := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
			return agentResultWithText("output before cost failure"), nil
		})

		resp, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			Resolved:       resolved,
			SessionID:      "missing-parent-session",
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.NoError(t, err)
		assert.False(t, resp.IsError)
		assert.Equal(t, "output before cost failure", resp.Content)
	})

	t.Run("response with text returns it", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent, resolved := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
			return agentResultWithText("the answer"), nil
		})

		resp, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			Resolved:       resolved,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.NoError(t, err)
		assert.False(t, resp.IsError)
		assert.Equal(t, "the answer", resp.Content)
	})

	t.Run("nil result returns error response", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent, resolved := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
			return nil, nil
		})

		resp, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			Resolved:       resolved,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.NoError(t, err)
		assert.True(t, resp.IsError)
		assert.Equal(t, "Sub-agent completed but produced no text output.", resp.Content)
	})

	t.Run("empty result returns error response", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent, resolved := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
			return &fantasy.AgentResult{}, nil
		})

		resp, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			Resolved:       resolved,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.NoError(t, err)
		assert.True(t, resp.IsError)
		assert.Equal(t, "Sub-agent completed but produced no text output.", resp.Content)
	})

	t.Run("ModelCfg.MaxTokens overrides default", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		model := Model{
			CatwalkCfg: catwalk.Model{
				DefaultMaxTokens: 4096,
			},
			ModelCfg: config.SelectedModel{
				Provider:  providerID,
				MaxTokens: 8192,
			},
		}
		agent := &mockSessionAgent{
			runFunc: func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
				assert.Equal(t, int64(8192), call.MaxOutputTokens)
				return agentResultWithText("ok"), nil
			},
		}
		resolved := resolvedAgent{Model: model, MaxTokens: maxTokensFor(config.Agent{}, model)}

		resp, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			Resolved:       resolved,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.NoError(t, err)
		assert.Equal(t, "ok", resp.Content)
	})

	t.Run("session creation failure with canceled context", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent, resolved := newMockAgent(providerID, 4096, nil)

		// Use a canceled context to trigger CreateTaskSession failure.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, err = coord.runSubAgent(ctx, subAgentParams{
			Agent:          agent,
			Resolved:       resolved,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.Error(t, err)
	})

	t.Run("provider not configured", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		// Agent references a provider that doesn't exist in config.
		agent, resolved := newMockAgent("unknown-provider", 4096, nil)

		_, err = coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			Resolved:       resolved,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "model provider not configured")
	})

	t.Run("agent run error returns error response", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent, resolved := newMockAgent(providerID, 4096, func(_ context.Context, _ SessionAgentCall) (*fantasy.AgentResult, error) {
			return nil, errors.New("provider request failed")
		})

		resp, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			Resolved:       resolved,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		// runSubAgent returns (errorResponse, nil) when agent.Run fails — not a Go error.
		require.NoError(t, err)
		assert.True(t, resp.IsError)
		assert.Equal(t, "Failed to generate response: provider request failed", resp.Content)
	})

	t.Run("cost propagation to parent session", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		agent, resolved := newMockAgent(providerID, 4096, func(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
			// Simulate the agent incurring cost by updating the child session.
			childSession, err := env.sessions.Get(ctx, call.SessionID)
			if err != nil {
				return nil, err
			}
			childSession.Cost = 0.05
			_, err = env.sessions.Save(ctx, childSession)
			if err != nil {
				return nil, err
			}
			return agentResultWithText("ok"), nil
		})

		_, err = coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			Resolved:       resolved,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "test",
			SessionTitle:   "Test",
		})
		require.NoError(t, err)

		updated, err := env.sessions.Get(t.Context(), parentSession.ID)
		require.NoError(t, err)
		assert.InDelta(t, 0.05, updated.Cost, 1e-9)
	})

	// TestRunSubAgent/child session is pre-approved pins a regression:
	// the generic dispatch path used to lose agentic_fetch's explicit
	// per-session grant on its child session. A child session has
	// no UI subscriber to ever answer its permission events, so without
	// this, any permission-gated tool a subagent uses (web_fetch,
	// web_search, or bash/edit inherited by "general") would block until
	// ctx is done rather than resolve. env.permissions defaults to
	// skip=true, which would mask that, so this uses its own
	// skip=false, rule-free service to isolate the child-session
	// grant from the global YOLO shortcut.
	t.Run("child session is pre-approved", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)
		coord.permissions = permission.NewPermissionService(env.workingDir, false, nil)

		parentSession, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		var granted bool
		agent, resolved := newMockAgent(providerID, 4096, func(ctx context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
			decision := coord.permissions.Gate(ctx, permission.GateRequest{
				SessionID:  call.SessionID,
				ToolCallID: "child-call",
				Access: permission.Access{
					Tool:   toolnames.WebFetch,
					Action: permission.ActionNetwork,
					URL:    "https://example.com",
				},
			})
			granted = decision.Allowed()
			return agentResultWithText("fetched"), nil
		})

		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()

		resp, err := coord.runSubAgent(ctx, subAgentParams{
			Agent:          agent,
			Resolved:       resolved,
			SessionID:      parentSession.ID,
			AgentMessageID: "msg-1",
			ToolCallID:     "call-1",
			Prompt:         "fetch it",
			SessionTitle:   "Test",
		})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		require.True(t, granted, "child session's permission request must resolve without an interactive prompt")
	})
}

func TestUpdateParentSessionCost(t *testing.T) {
	t.Run("accumulates cost correctly", func(t *testing.T) {
		env := testEnv(t)
		cfg, err := config.Init(env.workingDir, "", false)
		require.NoError(t, err)
		coord := &coordinator{cfg: cfg, sessions: env.sessions}

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		child, err := env.sessions.CreateTaskSession(t.Context(), "tool-1", parent.ID, "Child")
		require.NoError(t, err)

		// Set child cost.
		child.Cost = 0.10
		_, err = env.sessions.Save(t.Context(), child)
		require.NoError(t, err)

		err = coord.updateParentSessionCost(t.Context(), child.ID, parent.ID)
		require.NoError(t, err)

		updated, err := env.sessions.Get(t.Context(), parent.ID)
		require.NoError(t, err)
		assert.InDelta(t, 0.10, updated.Cost, 1e-9)
	})

	t.Run("accumulates multiple child costs", func(t *testing.T) {
		env := testEnv(t)
		cfg, err := config.Init(env.workingDir, "", false)
		require.NoError(t, err)
		coord := &coordinator{cfg: cfg, sessions: env.sessions}

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		child1, err := env.sessions.CreateTaskSession(t.Context(), "tool-1", parent.ID, "Child1")
		require.NoError(t, err)
		child1.Cost = 0.05
		_, err = env.sessions.Save(t.Context(), child1)
		require.NoError(t, err)

		child2, err := env.sessions.CreateTaskSession(t.Context(), "tool-2", parent.ID, "Child2")
		require.NoError(t, err)
		child2.Cost = 0.03
		_, err = env.sessions.Save(t.Context(), child2)
		require.NoError(t, err)

		err = coord.updateParentSessionCost(t.Context(), child1.ID, parent.ID)
		require.NoError(t, err)
		err = coord.updateParentSessionCost(t.Context(), child2.ID, parent.ID)
		require.NoError(t, err)

		updated, err := env.sessions.Get(t.Context(), parent.ID)
		require.NoError(t, err)
		assert.InDelta(t, 0.08, updated.Cost, 1e-9)
	})

	t.Run("child session not found", func(t *testing.T) {
		env := testEnv(t)
		cfg, err := config.Init(env.workingDir, "", false)
		require.NoError(t, err)
		coord := &coordinator{cfg: cfg, sessions: env.sessions}

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		err = coord.updateParentSessionCost(t.Context(), "non-existent", parent.ID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "get child session")
	})

	t.Run("parent session not found", func(t *testing.T) {
		env := testEnv(t)
		cfg, err := config.Init(env.workingDir, "", false)
		require.NoError(t, err)
		coord := &coordinator{cfg: cfg, sessions: env.sessions}

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)
		child, err := env.sessions.CreateTaskSession(t.Context(), "tool-1", parent.ID, "Child")
		require.NoError(t, err)

		err = coord.updateParentSessionCost(t.Context(), child.ID, "non-existent")
		require.Error(t, err)
		require.ErrorIs(t, err, session.ErrSessionNotFound)
	})

	t.Run("zero cost handled correctly", func(t *testing.T) {
		env := testEnv(t)
		cfg, err := config.Init(env.workingDir, "", false)
		require.NoError(t, err)
		coord := &coordinator{cfg: cfg, sessions: env.sessions}

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)
		child, err := env.sessions.CreateTaskSession(t.Context(), "tool-1", parent.ID, "Child")
		require.NoError(t, err)

		err = coord.updateParentSessionCost(t.Context(), child.ID, parent.ID)
		require.NoError(t, err)

		updated, err := env.sessions.Get(t.Context(), parent.ID)
		require.NoError(t, err)
		assert.InDelta(t, 0.0, updated.Cost, 1e-9)
	})
}

func TestGetProviderOptionsReasoningEffort(t *testing.T) {
	// Bedrock is Fantasy's Anthropic under a different provider name; options
	// must land under anthropic.Name so the Anthropic language model picks them up.
	tests := []struct {
		name         string
		providerType catwalk.Type
	}{
		{"anthropic honors reasoning_effort", catwalk.Type(anthropic.Name)},
		{"bedrock honors reasoning_effort", catwalk.Type(bedrock.Name)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			model := Model{
				CatwalkCfg: catwalk.Model{
					ID:              "claude-opus-4-7",
					CanReason:       true,
					ReasoningLevels: []string{"max"},
				},
				ModelCfg: config.SelectedModel{
					Provider:        "test",
					ReasoningEffort: "max",
				},
			}
			providerCfg := config.ProviderConfig{ID: "test", Type: tc.providerType}

			opts := getProviderOptions(model, providerCfg, "")

			raw, ok := opts[anthropic.Name]
			require.True(t, ok, "options should be keyed under anthropic.Name for type %q", tc.providerType)
			parsed, ok := raw.(*anthropic.ProviderOptions)
			require.True(t, ok)
			require.NotNil(t, parsed.Effort)
			assert.Equal(t, anthropic.Effort("max"), *parsed.Effort)
		})
	}
}

func TestGetProviderOptionsAnthropicUserID(t *testing.T) {
	model := Model{
		CatwalkCfg: catwalk.Model{ID: "claude-opus-4-7"},
		ModelCfg:   config.SelectedModel{Provider: "test"},
	}

	t.Run("anthropic sets extra_body.metadata.user_id from the prompt cache key", func(t *testing.T) {
		providerCfg := config.ProviderConfig{ID: "test", Type: catwalk.Type(anthropic.Name)}

		opts := getProviderOptions(model, providerCfg, "cache-key-1")

		raw, ok := opts[anthropic.Name]
		require.True(t, ok)
		parsed := raw.(*anthropic.ProviderOptions)
		metadata, ok := parsed.ExtraBody["metadata"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, buildAnthropicUserID("cache-key-1"), metadata["user_id"])
	})

	t.Run("bedrock does not get metadata.user_id", func(t *testing.T) {
		providerCfg := config.ProviderConfig{ID: "test", Type: catwalk.Type(bedrock.Name)}

		opts := getProviderOptions(model, providerCfg, "cache-key-2")

		raw, ok := opts[anthropic.Name]
		require.True(t, ok)
		parsed := raw.(*anthropic.ProviderOptions)
		_, hasMetadata := parsed.ExtraBody["metadata"]
		require.False(t, hasMetadata, "bedrock has a fixed request schema and does not support metadata.user_id")
	})

	t.Run("alibaba anthropic-compat endpoint does not get metadata.user_id", func(t *testing.T) {
		providerCfg := config.ProviderConfig{
			ID:   string(catwalk.InferenceProviderAlibabaSingapore),
			Type: catwalk.Type(anthropic.Name),
		}

		opts := getProviderOptions(model, providerCfg, "cache-key-3")

		raw, ok := opts[anthropic.Name]
		require.True(t, ok)
		parsed := raw.(*anthropic.ProviderOptions)
		_, hasMetadata := parsed.ExtraBody["metadata"]
		require.False(t, hasMetadata, "Alibaba's anthropic-compatible endpoint is not official Anthropic")
	})

	t.Run("empty prompt cache key sets no metadata", func(t *testing.T) {
		providerCfg := config.ProviderConfig{ID: "test", Type: catwalk.Type(anthropic.Name)}

		opts := getProviderOptions(model, providerCfg, "")

		raw, ok := opts[anthropic.Name]
		require.True(t, ok)
		parsed := raw.(*anthropic.ProviderOptions)
		_, hasMetadata := parsed.ExtraBody["metadata"]
		require.False(t, hasMetadata)
	})

	t.Run("does not override a user-configured metadata.user_id", func(t *testing.T) {
		modelWithOverride := Model{
			CatwalkCfg: catwalk.Model{ID: "claude-opus-4-7"},
			ModelCfg: config.SelectedModel{
				Provider: "test",
				ProviderOptions: map[string]any{
					"extra_body": map[string]any{
						"metadata": map[string]any{"user_id": "user-configured-id"},
					},
				},
			},
		}
		providerCfg := config.ProviderConfig{ID: "test", Type: catwalk.Type(anthropic.Name)}

		opts := getProviderOptions(modelWithOverride, providerCfg, "generated-key")

		raw, ok := opts[anthropic.Name]
		require.True(t, ok)
		parsed := raw.(*anthropic.ProviderOptions)
		metadata := parsed.ExtraBody["metadata"].(map[string]any)
		require.Equal(t, "user-configured-id", metadata["user_id"])
	})
}

func TestIsUnauthorized(t *testing.T) {
	t.Run("nil error", func(t *testing.T) {
		assert.False(t, isUnauthorized(nil))
	})

	t.Run("non-provider error", func(t *testing.T) {
		assert.False(t, isUnauthorized(errors.New("something broke")))
	})

	t.Run("provider error with 401", func(t *testing.T) {
		err := &fantasy.ProviderError{StatusCode: http.StatusUnauthorized, Message: "unauthorized"}
		assert.True(t, isUnauthorized(err))
	})

	t.Run("provider error with non-401", func(t *testing.T) {
		err := &fantasy.ProviderError{StatusCode: http.StatusForbidden, Message: "forbidden"}
		assert.False(t, isUnauthorized(err))
	})

	t.Run("wrapped provider error with 401", func(t *testing.T) {
		inner := &fantasy.ProviderError{StatusCode: http.StatusUnauthorized, Message: "expired"}
		err := fmt.Errorf("request failed: %w", inner)
		assert.True(t, isUnauthorized(err))
	})
}

func TestGetProviderOptionsReasoningEffortFallback(t *testing.T) {
	model := Model{
		CatwalkCfg: catwalk.Model{
			ID:              "glm-5.2",
			CanReason:       true,
			ReasoningLevels: []string{"high", "max"},
		},
		ModelCfg: config.SelectedModel{
			Provider: "zai",
		},
	}
	providerCfg := config.ProviderConfig{
		ID:   string(catwalk.InferenceProviderZAI),
		Type: openaicompat.Name,
	}

	opts := getProviderOptions(model, providerCfg, "")

	raw, ok := opts[openaicompat.Name]
	require.True(t, ok)
	parsed, ok := raw.(*openaicompat.ProviderOptions)
	require.True(t, ok)
	require.NotNil(t, parsed.ReasoningEffort)
	assert.Equal(t, "high", string(*parsed.ReasoningEffort))

	thinking, ok := parsed.ExtraBody["thinking"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "enabled", thinking["type"])
}

func TestGetProviderOptionsPromptCacheKey(t *testing.T) {
	t.Run("openai chat completions model sets prompt_cache_key", func(t *testing.T) {
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "not-a-responses-model"},
			ModelCfg:   config.SelectedModel{Provider: "openai"},
		}
		providerCfg := config.ProviderConfig{ID: "openai", Type: openai.Name}

		opts := getProviderOptions(model, providerCfg, "cache-key-1")

		raw, ok := opts[openai.Name]
		require.True(t, ok)
		parsed, ok := raw.(*openai.ProviderOptions)
		require.True(t, ok)
		require.NotNil(t, parsed.PromptCacheKey)
		require.Equal(t, "cache-key-1", *parsed.PromptCacheKey)
	})

	t.Run("openai responses model sets prompt_cache_key under the same options key", func(t *testing.T) {
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "gpt-5.2"},
			ModelCfg:   config.SelectedModel{Provider: "openai"},
		}
		providerCfg := config.ProviderConfig{ID: "openai", Type: openai.Name}

		opts := getProviderOptions(model, providerCfg, "cache-key-2")

		raw, ok := opts[openai.Name]
		require.True(t, ok)
		parsed, ok := raw.(*openai.ResponsesProviderOptions)
		require.True(t, ok, "responses models must produce *openai.ResponsesProviderOptions")
		require.NotNil(t, parsed.PromptCacheKey)
		require.Equal(t, "cache-key-2", *parsed.PromptCacheKey)
	})

	t.Run("azure shares the openai code path", func(t *testing.T) {
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "not-a-responses-model"},
			ModelCfg:   config.SelectedModel{Provider: "azure"},
		}
		providerCfg := config.ProviderConfig{ID: "azure", Type: azure.Name}

		opts := getProviderOptions(model, providerCfg, "cache-key-3")

		raw, ok := opts[openai.Name]
		require.True(t, ok, "azure options must be keyed under openai.Name")
		parsed, ok := raw.(*openai.ProviderOptions)
		require.True(t, ok)
		require.NotNil(t, parsed.PromptCacheKey)
		require.Equal(t, "cache-key-3", *parsed.PromptCacheKey)
	})

	t.Run("openai does not override a user-configured prompt_cache_key", func(t *testing.T) {
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "not-a-responses-model"},
			ModelCfg: config.SelectedModel{
				Provider:        "openai",
				ProviderOptions: map[string]any{"prompt_cache_key": "user-key"},
			},
		}
		providerCfg := config.ProviderConfig{ID: "openai", Type: openai.Name}

		opts := getProviderOptions(model, providerCfg, "generated-key")

		parsed := opts[openai.Name].(*openai.ProviderOptions)
		require.NotNil(t, parsed.PromptCacheKey)
		require.Equal(t, "user-key", *parsed.PromptCacheKey)
	})

	t.Run("openaicompat sets prompt_cache_key via extra_body and does not mirror for non-Copilot providers", func(t *testing.T) {
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "some-model"},
			ModelCfg:   config.SelectedModel{Provider: "custom"},
		}
		providerCfg := config.ProviderConfig{ID: "custom", Type: openaicompat.Name}

		opts := getProviderOptions(model, providerCfg, "cache-key-4")

		raw, ok := opts[openaicompat.Name]
		require.True(t, ok)
		parsed := raw.(*openaicompat.ProviderOptions)
		require.Equal(t, "cache-key-4", parsed.ExtraBody["prompt_cache_key"])

		_, mirrored := opts[openai.Name]
		require.False(t, mirrored, "non-Copilot openai-compat providers must not get an openai.Name mirror")
	})

	t.Run("openaicompat does not override a user-configured extra_body prompt_cache_key", func(t *testing.T) {
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "some-model"},
			ModelCfg: config.SelectedModel{
				Provider: "custom",
				ProviderOptions: map[string]any{
					"extra_body": map[string]any{"prompt_cache_key": "user-key", "other_field": "keep-me"},
				},
			},
		}
		providerCfg := config.ProviderConfig{ID: "custom", Type: openaicompat.Name}

		opts := getProviderOptions(model, providerCfg, "generated-key")

		parsed := opts[openaicompat.Name].(*openaicompat.ProviderOptions)
		require.Equal(t, "user-key", parsed.ExtraBody["prompt_cache_key"])
		require.Equal(t, "keep-me", parsed.ExtraBody["other_field"], "existing extra_body fields must survive")
	})

	t.Run("copilot responses model mirrors prompt_cache_key under openai.Name", func(t *testing.T) {
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "gpt-5.2"},
			ModelCfg:   config.SelectedModel{Provider: string(catwalk.InferenceProviderCopilot)},
		}
		providerCfg := config.ProviderConfig{ID: string(catwalk.InferenceProviderCopilot), Type: openaicompat.Name}

		opts := getProviderOptions(model, providerCfg, "cache-key-5")

		compatRaw, ok := opts[openaicompat.Name]
		require.True(t, ok)
		compatParsed := compatRaw.(*openaicompat.ProviderOptions)
		require.Equal(t, "cache-key-5", compatParsed.ExtraBody["prompt_cache_key"])

		respRaw, ok := opts[openai.Name]
		require.True(t, ok, "Copilot models dispatched via the Responses API must also get options keyed under openai.Name")
		respParsed, ok := respRaw.(*openai.ResponsesProviderOptions)
		require.True(t, ok)
		require.NotNil(t, respParsed.PromptCacheKey)
		require.Equal(t, "cache-key-5", *respParsed.PromptCacheKey)
	})

	t.Run("copilot responses model mirrors other extra_body fields too, not just prompt_cache_key", func(t *testing.T) {
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "gpt-5.2"},
			ModelCfg: config.SelectedModel{
				Provider: string(catwalk.InferenceProviderCopilot),
				ProviderOptions: map[string]any{
					"extra_body": map[string]any{
						"metadata":     map[string]any{"tenant": "acme"},
						"service_tier": "priority",
					},
				},
			},
		}
		providerCfg := config.ProviderConfig{ID: string(catwalk.InferenceProviderCopilot), Type: openaicompat.Name}

		opts := getProviderOptions(model, providerCfg, "cache-key-7")

		respRaw, ok := opts[openai.Name]
		require.True(t, ok)
		respParsed := respRaw.(*openai.ResponsesProviderOptions)
		require.NotNil(t, respParsed.PromptCacheKey)
		require.Equal(t, "cache-key-7", *respParsed.PromptCacheKey)
		require.Equal(t, "acme", respParsed.Metadata["tenant"])
		require.NotNil(t, respParsed.ServiceTier)
		require.Equal(t, "priority", string(*respParsed.ServiceTier))
	})

	t.Run("copilot non-responses model does not get an openai.Name mirror", func(t *testing.T) {
		model := Model{
			CatwalkCfg: catwalk.Model{ID: "gpt-4o"}, // not in copilotResponsesModels
			ModelCfg:   config.SelectedModel{Provider: string(catwalk.InferenceProviderCopilot)},
		}
		providerCfg := config.ProviderConfig{ID: string(catwalk.InferenceProviderCopilot), Type: openaicompat.Name}

		opts := getProviderOptions(model, providerCfg, "cache-key-6")

		_, ok := opts[openai.Name]
		require.False(t, ok, "only models listed in copilotResponsesModels should get the openai.Name mirror")
	})
}

func TestOpenaiCompatResponsesAPIFunc(t *testing.T) {
	t.Run("copilot returns a filter matching its responses models", func(t *testing.T) {
		fn, ok := openaiCompatResponsesAPIFunc(string(catwalk.InferenceProviderCopilot))
		require.True(t, ok)
		require.True(t, fn("gpt-5.2"))
		require.False(t, fn("gpt-4o"))
	})

	t.Run("unknown provider has no responses API filter", func(t *testing.T) {
		_, ok := openaiCompatResponsesAPIFunc("some-other-provider")
		require.False(t, ok)
	})
}

func TestWithPromptCacheKey(t *testing.T) {
	t.Run("sets the key on a nil map", func(t *testing.T) {
		result := withPromptCacheKey(nil, "key-1")
		require.Equal(t, "key-1", result["prompt_cache_key"])
	})

	t.Run("preserves existing extra_body fields", func(t *testing.T) {
		result := withPromptCacheKey(map[string]any{"reasoning_effort": "high"}, "key-1")
		require.Equal(t, "key-1", result["prompt_cache_key"])
		require.Equal(t, "high", result["reasoning_effort"])
	})

	t.Run("does not override an existing prompt_cache_key", func(t *testing.T) {
		result := withPromptCacheKey(map[string]any{"prompt_cache_key": "user-key"}, "generated-key")
		require.Equal(t, "user-key", result["prompt_cache_key"])
	})

	t.Run("empty generated key leaves extra_body untouched", func(t *testing.T) {
		result := withPromptCacheKey(map[string]any{"reasoning_effort": "high"}, "")
		_, ok := result["prompt_cache_key"]
		require.False(t, ok)
	})
}

// TestSubAgentInheritsWhetherAnyoneCanApprove pins the rule that keeps a
// headless run from stalling: whether a permission prompt can ever be
// answered depends on where the run started, not on how deep the work
// nested. Without the hand-down, a sub-agent dispatched by `angela run`
// parks on a prompt nothing is watching and burns the whole invocation.
func TestSubAgentInheritsWhetherAnyoneCanApprove(t *testing.T) {
	const providerID = "test-provider"
	providerCfg := config.ProviderConfig{ID: providerID}

	// dispatch runs one sub-agent under parentID and reports the
	// session the child actually ran in.
	dispatch := func(t *testing.T, coord *coordinator, parentID, callID string) string {
		t.Helper()
		var childID string
		agent, resolved := newMockAgent(providerID, 4096,
			func(_ context.Context, call SessionAgentCall) (*fantasy.AgentResult, error) {
				childID = call.SessionID
				return agentResultWithText("done"), nil
			})

		_, err := coord.runSubAgent(t.Context(), subAgentParams{
			Agent:          agent,
			Resolved:       resolved,
			SessionID:      parentID,
			AgentMessageID: "msg-" + callID,
			ToolCallID:     callID,
			Prompt:         "work",
			SessionTitle:   "Child",
		})
		require.NoError(t, err)
		require.NotEmpty(t, childID, "the sub-agent must have run in a session")
		return childID
	}

	t.Run("a headless run hands it down at every level", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)
		// Exactly what App.RunNonInteractive marks.
		env.permissions.SetSessionUnattended(parent.ID, true)

		child := dispatch(t, coord, parent.ID, "call-1")
		require.True(t, env.permissions.SessionUnattended(child),
			"a sub-agent of a headless run has no one to approve for it either")

		grandchild := dispatch(t, coord, child, "call-2")
		assert.True(t, env.permissions.SessionUnattended(grandchild),
			"nesting must not quietly regain an approver")
	})

	t.Run("a watched session hands down being watched", func(t *testing.T) {
		env := testEnv(t)
		coord := newTestCoordinator(t, env, providerID, providerCfg)

		parent, err := env.sessions.Create(t.Context(), "Parent")
		require.NoError(t, err)

		child := dispatch(t, coord, parent.ID, "call-1")
		assert.False(t, env.permissions.SessionUnattended(child),
			"a sub-agent under a TUI session must still be able to ask the user")
	})
}
