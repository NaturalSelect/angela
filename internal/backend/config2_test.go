package backend

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/stretchr/testify/require"
)

// TestBackendConfig_WorkspaceNotFound covers the shared
// "ws, err := b.GetWorkspace(workspaceID); if err != nil return err"
// guard at the top of every config.go method, using the same
// zero-workspace backend fixture the rest of this package uses.
func TestBackendConfig_WorkspaceNotFound(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)

	tests := []struct {
		name string
		call func(t *testing.T) error
	}{
		{"SetConfigField", func(t *testing.T) error {
			return b.SetConfigField("nope", config.ScopeGlobal, "options.debug", true)
		}},
		{"RemoveConfigField", func(t *testing.T) error {
			return b.RemoveConfigField("nope", config.ScopeGlobal, "options.debug")
		}},
		{"UpdatePreferredModel", func(t *testing.T) error {
			return b.UpdatePreferredModel("nope", config.ScopeGlobal, config.SlotMain, config.SelectedModel{})
		}},
		{"RecordRecentModel", func(t *testing.T) error {
			return b.RecordRecentModel("nope", config.ScopeGlobal, config.SlotMain, config.SelectedModel{})
		}},
		{"PruneRecentModels", func(t *testing.T) error {
			return b.PruneRecentModels("nope", config.ScopeGlobal, config.SlotMain, []config.SelectedModel{{Provider: "p", Model: "m"}})
		}},
		{"SetCompactMode", func(t *testing.T) error {
			return b.SetCompactMode("nope", config.ScopeGlobal, true)
		}},
		{"SetProviderAPIKey", func(t *testing.T) error {
			return b.SetProviderAPIKey("nope", config.ScopeGlobal, "openai", "key")
		}},
		{"UpsertProviderModel", func(t *testing.T) error {
			return b.UpsertProviderModel("nope", config.ScopeGlobal, "openai", config.ProviderModel{})
		}},
		{"ImportCopilot", func(t *testing.T) error {
			_, _, err := b.ImportCopilot("nope")
			return err
		}},
		{"RefreshOAuthToken", func(t *testing.T) error {
			return b.RefreshOAuthToken(t.Context(), "nope", config.ScopeGlobal, "openai")
		}},
		{"ProjectNeedsInitialization", func(t *testing.T) error {
			_, err := b.ProjectNeedsInitialization("nope")
			return err
		}},
		{"MarkProjectInitialized", func(t *testing.T) error {
			return b.MarkProjectInitialized("nope")
		}},
		{"InitializePrompt", func(t *testing.T) error {
			_, err := b.InitializePrompt("nope")
			return err
		}},
		{"ReadSkill", func(t *testing.T) error {
			_, _, err := b.ReadSkill(t.Context(), "nope", "skill-id")
			return err
		}},
		{"ListSkills", func(t *testing.T) error {
			_, err := b.ListSkills("nope")
			return err
		}},
		{"EnableDockerMCP", func(t *testing.T) error {
			return b.EnableDockerMCP(t.Context(), "nope")
		}},
		{"DisableDockerMCP", func(t *testing.T) error {
			return b.DisableDockerMCP("nope")
		}},
		{"RefreshMCPTools", func(t *testing.T) error {
			return b.RefreshMCPTools(t.Context(), "nope", "server")
		}},
		{"ReadMCPResource", func(t *testing.T) error {
			_, err := b.ReadMCPResource(t.Context(), "nope", "server", "uri")
			return err
		}},
		{"GetMCPPrompt", func(t *testing.T) error {
			_, err := b.GetMCPPrompt("nope", "client", "prompt", nil)
			return err
		}},
		{"ListMCPPrompts", func(t *testing.T) error {
			_, err := b.ListMCPPrompts("nope")
			return err
		}},
		{"GetWorkingDir", func(t *testing.T) error {
			_, err := b.GetWorkingDir("nope")
			return err
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.ErrorIs(t, tc.call(t), ErrWorkspaceNotFound)
		})
	}
}

// TestBackendConfig_RecentModels drives RecordRecentModel and
// PruneRecentModels (both permissive: no provider validation) against a
// real ConfigStore and confirms each publishes ConfigChanged.
func TestBackendConfig_RecentModels(t *testing.T) {
	b, ws, evc := newPublishingWorkspace(t)

	model := config.SelectedModel{Provider: "openai", Model: "gpt-5"}
	require.NoError(t, b.RecordRecentModel(ws.ID, config.ScopeGlobal, config.SlotMain, model))
	awaitConfigChanged(t, evc, ws.ID)

	require.NoError(t, b.PruneRecentModels(ws.ID, config.ScopeGlobal, config.SlotMain, []config.SelectedModel{model}))
	awaitConfigChanged(t, evc, ws.ID)
}

// TestBackendConfig_UpsertProviderModel exercises both the validation
// error (unknown provider) and the success path. The success path
// registers a provider directly in the in-memory active config so
// isKnownProvider resolves it without touching the network-backed
// known-provider catalog.
func TestBackendConfig_UpsertProviderModel(t *testing.T) {
	b, ws, evc := newPublishingWorkspace(t)

	err := b.UpsertProviderModel(ws.ID, config.ScopeGlobal, "no-such-provider", config.ProviderModel{Model: catwalk.Model{ID: "m1"}})
	require.Error(t, err)

	ws.Cfg.Config().Providers.Set("test-provider", config.ProviderConfig{ID: "test-provider", Name: "Test"})
	err = b.UpsertProviderModel(ws.ID, config.ScopeGlobal, "test-provider", config.ProviderModel{Model: catwalk.Model{ID: "m1"}})
	require.NoError(t, err)
	awaitConfigChanged(t, evc, ws.ID)
}

func TestBackendConfig_ProjectNeedsInitialization(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	needs, err := b.ProjectNeedsInitialization(ws.ID)
	require.NoError(t, err)
	require.False(t, needs, "an empty directory with no visible source files has nothing to initialize")
}

func TestBackendConfig_InitializePrompt(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	prompt, err := b.InitializePrompt(ws.ID)
	require.NoError(t, err)
	require.NotEmpty(t, prompt)
}

func TestBackendConfig_GetWorkingDir(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	dir, err := b.GetWorkingDir(ws.ID)
	require.NoError(t, err)
	require.True(t, filepath.IsAbs(dir))
}

// TestBackendConfig_Skills writes a real skill file to disk before the
// workspace is created (so skills.DiscoverFromConfig picks it up), then
// exercises both ListSkills and the happy/error paths of ReadSkill
// against it.
func TestBackendConfig_Skills(t *testing.T) {
	xdgIsolated(t)

	cwd := t.TempDir()
	dataDir := t.TempDir()
	writeSkillFile(t, cwd, "demo-skill", "A demo skill for tests.")

	b := New(t.Context(), nil, func() {})
	b.SetCreateGrace(2 * time.Second)
	t.Cleanup(func() { drainBackend(t, b) })

	cid := newClientID(t)
	ws, _, err := b.CreateWorkspace(protoWS(cwd, dataDir, cid))
	require.NoError(t, err)

	skills, err := b.ListSkills(ws.ID)
	require.NoError(t, err)
	skillID, ok := skillIDByName(skills, "demo-skill")
	require.True(t, ok, "ListSkills must surface the discovered skill")

	content, result, err := b.ReadSkill(t.Context(), ws.ID, skillID)
	require.NoError(t, err)
	require.Contains(t, string(content), "A demo skill for tests.")
	require.Equal(t, "demo-skill", result.Name)

	_, _, err = b.ReadSkill(t.Context(), ws.ID, "no-such-skill")
	require.Error(t, err)
}

func writeSkillFile(t *testing.T, workingDir, name, desc string) {
	t.Helper()
	skillDir := filepath.Join(workingDir, ".agents", "skills", name)
	require.NoError(t, os.MkdirAll(skillDir, 0o755))
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n" + desc + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644))
}

// skillIDByName looks up a skill's ID (its resolved file path) by
// display name, since ReadSkill keys on ID rather than Name.
func skillIDByName(skills []proto.SkillInfo, name string) (string, bool) {
	for _, s := range skills {
		if s.Name == name {
			return s.ID, true
		}
	}
	return "", false
}

// TestBackendConfig_EnableDockerMCP_Unavailable covers the fast-fail
// branch: this test environment has no working `docker mcp` CLI plugin,
// so PrepareDockerMCPConfig must reject before any MCP client is ever
// started.
func TestBackendConfig_EnableDockerMCP_Unavailable(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	err := b.EnableDockerMCP(t.Context(), ws.ID)
	require.Error(t, err)
}

func TestBackendConfig_RefreshMCPTools(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)
	require.NoError(t, b.RefreshMCPTools(t.Context(), ws.ID, "no-such-server"))
}

func TestBackendConfig_ReadMCPResource(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)
	_, err := b.ReadMCPResource(t.Context(), ws.ID, "no-such-server", "any-uri")
	require.Error(t, err)
}

func TestBackendConfig_GetMCPPrompt(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)
	_, err := b.GetMCPPrompt(ws.ID, "no-such-client", "no-such-prompt", nil)
	require.Error(t, err)
}

func TestBackendConfig_ListMCPPrompts(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)
	prompts, err := b.ListMCPPrompts(ws.ID)
	require.NoError(t, err)
	require.Empty(t, prompts)
}
