package app

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy/providers/openaicompat"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/db"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/NaturalSelect/angela/internal/update"
	"github.com/stretchr/testify/require"
)

// mustConnectTestDB opens a real, migrated SQLite database in dataDir for
// New to wrap, mirroring how production connects before calling New.
// Cleanup releases the pooled connection.
func mustConnectTestDB(t *testing.T, dataDir string) *sql.DB {
	t.Helper()
	conn, err := db.Connect(context.Background(), dataDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Release(dataDir) })
	return conn
}

// stubUpdateDefault replaces update.Default with a client that always
// fails, so New's background checkForUpdates goroutine never attempts a
// real network call during these tests.
func stubUpdateDefault(t *testing.T) {
	t.Helper()
	original := update.Default
	update.Default = fakeUpdateClient{err: errors.New("network disabled in test")}
	t.Cleanup(func() { update.Default = original })
}

// newUnconfiguredStore returns a ConfigStore with no providers, so
// Config.IsConfigured reports false and New skips coder-agent
// initialization.
func newUnconfiguredStore(dataDir string) *config.ConfigStore {
	cfg := &config.Config{
		Options:   &config.Options{DataDirectory: dataDir},
		Providers: csync.NewMap[string, config.ProviderConfig](),
	}
	return config.NewTestStore(cfg)
}

// newOfflineConfiguredStore returns a ConfigStore with a single
// openai-compatible provider pointed at an unroutable local address, so
// Config.IsConfigured reports true and agent.NewCoordinator can build a
// real coordinator without ever making network I/O. It mirrors the recipe
// internal/agent/agenttest.NewCoordinator uses.
func newOfflineConfiguredStore(dataDir string) *config.ConfigStore {
	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("test-openai-compat", config.ProviderConfig{
		ID:      "test-openai-compat",
		Name:    "Test",
		Type:    openaicompat.Name,
		BaseURL: "http://127.0.0.1:0/v1",
		APIKey:  "test",
		Models:  []config.ProviderModel{{Model: catwalk.Model{ID: "test-model", DefaultMaxTokens: 4096}}},
	})
	selected := config.SelectedModel{Provider: "test-openai-compat", Model: "test-model"}
	cfg := &config.Config{
		Options:   &config.Options{DataDirectory: dataDir},
		Providers: providers,
		Slots: map[config.SlotName]config.SelectedModel{
			config.SlotMain:  selected,
			config.SlotChore: selected,
		},
	}
	store := config.NewTestStore(cfg)
	store.SetupAgents()

	// Keep coordinator construction cheap and free of sub-agent tool
	// wiring, mirroring agenttest.NewCoordinator.
	coderCfg := store.Config().Agents[config.AgentCoder]
	coderCfg.AllowedTools = &config.AllowedToolSet{Kind: config.ToolSetScope}
	store.Config().Agents[config.AgentCoder] = coderCfg

	return store
}

// TestNew_Unconfigured_WiresCoreServicesAndSkipsAgentInit drives New
// through its full construction path with no providers configured: it
// wires every core service against a real (temp) database, runs
// setupEvents for real, and returns before ever touching InitCoderAgent or
// the LSP callback wiring.
func TestNew_Unconfigured_WiresCoreServicesAndSkipsAgentInit(t *testing.T) {
	stubUpdateDefault(t)

	dataDir := t.TempDir()
	conn := mustConnectTestDB(t, dataDir)
	store := newUnconfiguredStore(dataDir)
	skillsMgr := skills.NewManager(nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := New(ctx, conn, store, skillsMgr)
	require.NoError(t, err)
	require.NotNil(t, application)
	defer application.Shutdown()

	require.NotNil(t, application.Sessions)
	require.NotNil(t, application.Messages)
	require.NotNil(t, application.History)
	require.NotNil(t, application.Permissions)
	require.NotNil(t, application.Questions)
	require.NotNil(t, application.FileTracker)
	require.NotNil(t, application.Undo)
	require.NotNil(t, application.LSPManager)
	require.Nil(t, application.AgentCoordinator, "unconfigured config must not initialize a coder agent")

	// setupEvents wires app.Sessions into app.events for real; creating a
	// session should surface as an event on Events.
	events := application.Events(ctx)
	time.Sleep(20 * time.Millisecond)

	_, err = application.Sessions.Create(ctx, "hello")
	require.NoError(t, err)

	select {
	case ev := <-events:
		require.NotNil(t, ev.Payload)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for session creation to fan into app.Events")
	}
}

// TestNew_Configured_InitializesCoderAgent drives New with a single
// offline-resolvable provider configured, so Config.IsConfigured reports
// true and New must run InitCoderAgent through to a real agent.Coordinator.
func TestNew_Configured_InitializesCoderAgent(t *testing.T) {
	stubUpdateDefault(t)

	dataDir := t.TempDir()
	conn := mustConnectTestDB(t, dataDir)
	store := newOfflineConfiguredStore(dataDir)
	skillsMgr := skills.NewManager(nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := New(ctx, conn, store, skillsMgr)
	require.NoError(t, err)
	require.NotNil(t, application)
	defer application.Shutdown()

	require.NotNil(t, application.AgentCoordinator, "configured provider must initialize a coder agent")
	require.False(t, application.AgentCoordinator.IsSessionBusy("nonexistent-session"))
}

// TestNew_ConfiguredButNoCoderAgent_ReturnsWrappedError verifies that
// New wraps the InitCoderAgent error rather than swallowing it: a
// config with an enabled provider (so IsConfigured is true) but no
// coder agent set up (SetupAgents was never called) must fail New
// itself instead of returning a half-initialized App.
func TestNew_ConfiguredButNoCoderAgent_ReturnsWrappedError(t *testing.T) {
	stubUpdateDefault(t)

	dataDir := t.TempDir()
	conn := mustConnectTestDB(t, dataDir)

	providers := csync.NewMap[string, config.ProviderConfig]()
	providers.Set("test-openai-compat", config.ProviderConfig{
		ID:      "test-openai-compat",
		Name:    "Test",
		Type:    openaicompat.Name,
		BaseURL: "http://127.0.0.1:0/v1",
		APIKey:  "test",
		Models:  []config.ProviderModel{{Model: catwalk.Model{ID: "test-model", DefaultMaxTokens: 4096}}},
	})
	cfg := &config.Config{
		Options:   &config.Options{DataDirectory: dataDir},
		Providers: providers,
	}
	// Deliberately skip store.SetupAgents(), so cfg.Agents[config.AgentCoder]
	// stays unset and initCoderAgent fails.
	store := config.NewTestStore(cfg)
	skillsMgr := skills.NewManager(nil, nil, nil)

	application, err := New(context.Background(), conn, store, skillsMgr)
	require.Nil(t, application)
	require.ErrorContains(t, err, "failed to initialize coder agent")
}

// TestApp_RunNonInteractive_InvalidModelOverride verifies that
// RunNonInteractive surfaces a wrapped model-override error before
// touching MCP initialization, UpdateModels, or session resolution:
// with an offline-resolvable coordinator successfully (re)initialized,
// an unmatched --model flag must fail fast without ever dispatching an
// agent run.
func TestApp_RunNonInteractive_InvalidModelOverride(t *testing.T) {
	stubUpdateDefault(t)

	dataDir := t.TempDir()
	conn := mustConnectTestDB(t, dataDir)
	store := newOfflineConfiguredStore(dataDir)
	skillsMgr := skills.NewManager(nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, err := New(ctx, conn, store, skillsMgr)
	require.NoError(t, err)
	defer application.Shutdown()

	err = application.RunNonInteractive(ctx, io.Discard, "prompt", "does-not-exist-model", "", true, "", false)
	require.ErrorContains(t, err, "failed to override models")
}

// TestNew_InvalidPermissionsPromptPolicy verifies New rejects an
// unrecognized permissions.prompt value before constructing any service,
// matching the validation ParsePromptPolicy performs.
func TestNew_InvalidPermissionsPromptPolicy(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	conn := mustConnectTestDB(t, dataDir)
	cfg := &config.Config{
		Options:     &config.Options{DataDirectory: dataDir},
		Providers:   csync.NewMap[string, config.ProviderConfig](),
		Permissions: &config.Permissions{Prompt: "not-a-real-policy"},
	}
	store := config.NewTestStore(cfg)
	skillsMgr := skills.NewManager(nil, nil, nil)

	application, err := New(context.Background(), conn, store, skillsMgr)
	require.Nil(t, application)
	require.ErrorContains(t, err, "invalid permissions prompt policy")
}

// TestNew_InvalidPermissionRule verifies New surfaces a CompilePolicy error
// for a malformed permission rule pattern.
func TestNew_InvalidPermissionRule(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	conn := mustConnectTestDB(t, dataDir)
	cfg := &config.Config{
		Options:   &config.Options{DataDirectory: dataDir},
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Permissions: &config.Permissions{
			Rules: []permission.Rule{{Pattern: "["}},
		},
	}
	store := config.NewTestStore(cfg)
	skillsMgr := skills.NewManager(nil, nil, nil)

	application, err := New(context.Background(), conn, store, skillsMgr)
	require.Nil(t, application)
	require.ErrorContains(t, err, "invalid pattern")
}
