package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/NaturalSelect/angela/internal/undo"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// newAWFixtureWithStore extends newAWFixture with a real config store
// for methods that read AppWorkspace.store directly (MCP passthroughs
// and config accessors). Not parallel-safe: newTestConfigStore calls
// t.Setenv.
func newAWFixtureWithStore(t *testing.T) (*awFixture, *config.ConfigStore) {
	t.Helper()
	fx := newAWFixture(t)
	store := newTestConfigStore(t)
	fx.ws = NewAppWorkspace(fx.app, store)
	return fx, store
}

func TestAppWorkspace_PreviewUndo(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		mockUndo := NewMockUndoService(gomock.NewController(t))
		fx.app.Undo = mockUndo
		want := undo.Preview{CutMessageID: "m1", PoppedText: "hi", MessageCount: 2}
		mockUndo.EXPECT().Preview(gomock.Any(), "s1").Return(want, nil)

		got, err := fx.ws.PreviewUndo(t.Context(), "s1")
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("propagates service error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		mockUndo := NewMockUndoService(gomock.NewController(t))
		fx.app.Undo = mockUndo
		boom := errors.New("nothing to undo")
		mockUndo.EXPECT().Preview(gomock.Any(), "s1").Return(undo.Preview{}, boom)

		_, err := fx.ws.PreviewUndo(t.Context(), "s1")
		require.ErrorIs(t, err, boom)
	})
}

func TestAppWorkspace_Undo(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		mockUndo := NewMockUndoService(gomock.NewController(t))
		fx.app.Undo = mockUndo
		want := undo.Result{PoppedText: "hi", Reverted: []string{"/a"}}
		mockUndo.EXPECT().Undo(gomock.Any(), "s1", "m1").Return(want, nil)

		got, err := fx.ws.Undo(t.Context(), "s1", "m1")
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("propagates service error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		mockUndo := NewMockUndoService(gomock.NewController(t))
		fx.app.Undo = mockUndo
		mockUndo.EXPECT().Undo(gomock.Any(), "s1", "m1").Return(undo.Result{}, undo.ErrStale)

		_, err := fx.ws.Undo(t.Context(), "s1", "m1")
		require.ErrorIs(t, err, undo.ErrStale)
	})
}

// TestAppWorkspace_LSPStart uses a path outside the manager's working
// directory so Start returns before it would ever touch a real server
// binary, keeping this a fast, deterministic passthrough check.
func TestAppWorkspace_LSPStart(t *testing.T) {
	// Not parallel: newTestConfigStore calls t.Setenv.
	fx := newAWFixture(t)
	store := newTestConfigStore(t)
	fx.app.LSPManager = lsp.NewManager(store)

	fx.ws.LSPStart(t.Context(), "/definitely/outside/workdir/file.go")

	require.Zero(t, fx.app.LSPManager.Clients().Len())
}

func TestAppWorkspace_LSPStopAll(t *testing.T) {
	// Not parallel: newTestConfigStore calls t.Setenv.
	fx := newAWFixture(t)
	store := newTestConfigStore(t)
	fx.app.LSPManager = lsp.NewManager(store)

	require.NotPanics(t, func() { fx.ws.LSPStopAll(t.Context()) })
	require.Zero(t, fx.app.LSPManager.Clients().Len())
}

// TestAppWorkspace_LSPGetStates reads the process-global LSP state
// registry (internal/app package var), which nothing in this test
// binary populates without starting a real LSP client.
func TestAppWorkspace_LSPGetStates(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	require.Empty(t, fx.ws.LSPGetStates())
}

func TestAppWorkspace_LSPGetDiagnosticCounts_UnknownName(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	require.Equal(t, lsp.DiagnosticCounts{}, fx.ws.LSPGetDiagnosticCounts("does-not-exist"))
}

func TestAppWorkspace_ListSkills(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	sk := &skills.Skill{SkillFilePath: "my-skill", Name: "My Skill", Description: "does things", UserInvocable: true}
	fx.app.Skills = skills.NewManager([]*skills.Skill{sk}, []*skills.Skill{sk}, nil, skills.WithWorkingDir("/work"))

	got, err := fx.ws.ListSkills(t.Context())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "my-skill", got[0].ID)
	require.Equal(t, "My Skill", got[0].Name)
	require.Equal(t, "does things", got[0].Description)
	require.True(t, got[0].UserInvocable)
}

func TestAppWorkspace_ReadSkill(t *testing.T) {
	t.Parallel()

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		fx.app.Skills = skills.NewManager(nil, nil, nil)

		_, _, err := fx.ws.ReadSkill(t.Context(), "missing")
		require.ErrorIs(t, err, skills.ErrSkillNotFound)
	})

	t.Run("found", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		// ReadContent reads the skill file from disk, so the path must
		// resolve to a real file rather than a synthetic ID.
		path := filepath.Join(t.TempDir(), "SKILL.md")
		require.NoError(t, os.WriteFile(path, []byte("do the thing"), 0o644))
		sk := &skills.Skill{SkillFilePath: path, Name: "My Skill"}
		fx.app.Skills = skills.NewManager([]*skills.Skill{sk}, []*skills.Skill{sk}, nil)

		content, result, err := fx.ws.ReadSkill(t.Context(), path)
		require.NoError(t, err)
		require.Equal(t, "do the thing", string(content))
		require.Equal(t, "My Skill", result.Name)
	})
}

func TestAppWorkspace_MCPGetStates(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	got := fx.ws.MCPGetStates()
	require.IsType(t, map[string]mcp.ClientInfo{}, got)
}

func TestAppWorkspace_MCPRefreshPrompts_NoSession(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	require.NotPanics(t, func() { fx.ws.MCPRefreshPrompts(t.Context(), "nonexistent-server-xyz") })
}

func TestAppWorkspace_MCPRefreshResources_NoSession(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	require.NotPanics(t, func() { fx.ws.MCPRefreshResources(t.Context(), "nonexistent-server-xyz") })
}

func TestAppWorkspace_RefreshMCPTools_NoSession(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)
	require.NotPanics(t, func() { fx.ws.RefreshMCPTools(t.Context(), "nonexistent-server-xyz") })
}

func TestAppWorkspace_ReadMCPResource_NotAvailable(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)

	got, err := fx.ws.ReadMCPResource(t.Context(), "nonexistent-server-xyz", "file:///a")
	require.ErrorContains(t, err, "not available")
	require.Nil(t, got)
}

func TestAppWorkspace_ListMCPPrompts_Empty(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)

	got, err := fx.ws.ListMCPPrompts(t.Context())
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestAppWorkspace_GetMCPPrompt_NotAvailable(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)

	_, err := fx.ws.GetMCPPrompt("nonexistent-server-xyz", "review", nil)
	require.ErrorContains(t, err, "not available")
}

func TestAppWorkspace_EnableDockerMCP_DockerUnavailable(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)

	// This sandbox has no docker binary, so PrepareDockerMCPConfig fails
	// before ever touching the mcp package's global client registry.
	err := fx.ws.EnableDockerMCP(t.Context())
	require.ErrorContains(t, err, "docker mcp is not available")
}

func TestAppWorkspace_DisableDockerMCP(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)

	require.NoError(t, fx.ws.DisableDockerMCP())
}

func TestAppWorkspace_MCPAuthenticate_NotConfigured(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)

	err := fx.ws.MCPAuthenticate(t.Context(), "nonexistent-server-xyz")
	require.ErrorContains(t, err, "not found in configuration")
}

func TestAppWorkspace_MCPPendingAuth_Empty(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)
	require.Empty(t, fx.ws.MCPPendingAuth())
}

func TestAppWorkspace_MCPAuthURL_UnknownName(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	require.Empty(t, fx.ws.MCPAuthURL("nonexistent-server-xyz"))
}

func TestAppWorkspace_MCPEnable_NotConfigured(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)

	err := fx.ws.MCPEnable(t.Context(), "nonexistent-server-xyz")
	require.ErrorContains(t, err, "not found in configuration")
}

func TestAppWorkspace_MCPDisable_NeverStarted(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)

	// DisableSingle only tears down local runtime state and never checks
	// the config, so disabling a server that was never started succeeds
	// as a no-op rather than erroring.
	require.NoError(t, fx.ws.MCPDisable("nonexistent-server-xyz"))
}

// TestAppWorkspace_Shutdown wires just enough of app.App (LSPManager,
// AgentCoordinator) for the production Shutdown path to run without
// touching a real database, matching the tolerances already pinned by
// TestApp_Shutdown_CancelsCoordinatorAndRunsCleanup in internal/app.
func TestAppWorkspace_Shutdown(t *testing.T) {
	// Not parallel: newTestConfigStore calls t.Setenv.
	fx := newAWFixture(t)
	store := newTestConfigStore(t)
	fx.app.LSPManager = lsp.NewManager(store)
	fx.coord.EXPECT().CancelAll()
	fx.messages.EXPECT().FlushAll(gomock.Any()).Return(nil)

	require.NotPanics(t, func() { fx.ws.Shutdown() })
}

func TestAppWorkspace_App(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	require.Same(t, fx.app, fx.ws.App())
}

func TestAppWorkspace_Store(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, store := newAWFixtureWithStore(t)
	require.Same(t, store, fx.ws.Store())
}

func TestAppWorkspace_Config(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, store := newAWFixtureWithStore(t)
	require.Same(t, store.Config(), fx.ws.Config())
}

func TestAppWorkspace_WorkingDir(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, store := newAWFixtureWithStore(t)
	require.Equal(t, store.WorkingDir(), fx.ws.WorkingDir())
	require.True(t, filepath.IsAbs(fx.ws.WorkingDir()))
}

func TestAppWorkspace_Resolver(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)
	require.NotNil(t, fx.ws.Resolver())
}

func TestAppWorkspace_UpdatePreferredModel(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, store := newAWFixtureWithStore(t)
	sel := config.SelectedModel{Provider: "acme", Model: "m1"}

	require.NoError(t, fx.ws.UpdatePreferredModel(config.ScopeGlobal, config.SlotMain, sel))
	require.Equal(t, sel, store.Config().Slots[config.SlotMain])
}

func TestAppWorkspace_RecordRecentModel(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, store := newAWFixtureWithStore(t)
	sel := config.SelectedModel{Provider: "acme", Model: "m1"}

	require.NoError(t, fx.ws.RecordRecentModel(config.ScopeGlobal, config.SlotMain, sel))
	require.Equal(t, sel, store.Config().RecentModels[config.SlotMain][0])
}

func TestAppWorkspace_PruneRecentModels(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, store := newAWFixtureWithStore(t)
	sel := config.SelectedModel{Provider: "acme", Model: "m1"}
	require.NoError(t, fx.ws.RecordRecentModel(config.ScopeGlobal, config.SlotMain, sel))
	require.Len(t, store.Config().RecentModels[config.SlotMain], 1)

	require.NoError(t, fx.ws.PruneRecentModels(config.ScopeGlobal, config.SlotMain, []config.SelectedModel{sel}))
	require.Empty(t, store.Config().RecentModels[config.SlotMain])
}

func TestAppWorkspace_UpsertProviderModel_MissingID(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)

	err := fx.ws.UpsertProviderModel(config.ScopeGlobal, "acme", config.ProviderModel{})
	require.ErrorContains(t, err, "required")
}

func TestAppWorkspace_SetCompactMode(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, store := newAWFixtureWithStore(t)

	require.NoError(t, fx.ws.SetCompactMode(config.ScopeGlobal, true))
	require.True(t, store.Config().Options.TUI.CompactMode)
}

// TestAppWorkspace_SetProviderAPIKey seeds a fully-specified custom
// provider (disable_default_providers skips the network fetch to the
// real Catwalk catalog): SetProviderAPIKey only updates a provider that
// survives configureProviders, which drops any custom entry lacking a
// base URL.
func TestAppWorkspace_SetProviderAPIKey(t *testing.T) {
	// Not parallel: config.Load isolates HOME/XDG via t.Setenv.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"), []byte(`{
		"options": {"disable_default_providers": true},
		"providers": {"acme": {"base_url": "http://127.0.0.1:0/v1",
			"models": [{"id": "m1", "name": "M1"}]}}
	}`), 0o644))
	store := newTestConfigStoreInDir(t, dir)
	fx := newAWFixture(t)
	fx.ws = NewAppWorkspace(fx.app, store)

	require.NoError(t, fx.ws.SetProviderAPIKey(config.ScopeGlobal, "acme", "sk-secret"))
	pc, ok := store.Config().Providers.Get("acme")
	require.True(t, ok)
	require.Equal(t, "sk-secret", pc.APIKey)
}

func TestAppWorkspace_SetConfigField(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, store := newAWFixtureWithStore(t)

	require.NoError(t, fx.ws.SetConfigField(config.ScopeGlobal, "options.debug", true))
	require.True(t, store.Config().Options.Debug)
}

func TestAppWorkspace_RemoveConfigField(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, store := newAWFixtureWithStore(t)
	require.NoError(t, fx.ws.SetConfigField(config.ScopeGlobal, "options.debug", true))

	require.NoError(t, fx.ws.RemoveConfigField(config.ScopeGlobal, "options.debug"))
	require.False(t, store.Config().Options.Debug)
}

func TestAppWorkspace_ImportCopilot_NothingOnDisk(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)

	tok, imported := fx.ws.ImportCopilot()
	require.False(t, imported)
	require.Nil(t, tok)
}

func TestAppWorkspace_RefreshOAuthToken_UnknownProvider(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)

	err := fx.ws.RefreshOAuthToken(t.Context(), config.ScopeGlobal, "nonexistent-provider-xyz")
	require.ErrorContains(t, err, "not found")
}

// TestAppWorkspace_ProjectLifecycle exercises ProjectNeedsInitialization
// and MarkProjectInitialized together: the first only reports true for a
// non-empty, uninitialized directory, and the second is what flips it
// back to false, so testing them apart would leave one side a tautology.
func TestAppWorkspace_ProjectLifecycle(t *testing.T) {
	// Not parallel: newTestConfigStoreInDir calls t.Setenv.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644))
	store := newTestConfigStoreInDir(t, dir)
	fx := newAWFixture(t)
	fx.ws = NewAppWorkspace(fx.app, store)

	needs, err := fx.ws.ProjectNeedsInitialization()
	require.NoError(t, err)
	require.True(t, needs)

	require.NoError(t, fx.ws.MarkProjectInitialized())

	needs, err = fx.ws.ProjectNeedsInitialization()
	require.NoError(t, err)
	require.False(t, needs)
}

func TestAppWorkspace_InitializePrompt(t *testing.T) {
	// Not parallel: newAWFixtureWithStore calls t.Setenv.
	fx, _ := newAWFixtureWithStore(t)

	out, err := fx.ws.InitializePrompt()
	require.NoError(t, err)
	require.NotEmpty(t, out)
}
