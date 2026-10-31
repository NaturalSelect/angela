package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/stretchr/testify/require"
)

// reloadFixture is an App backed by a real config store loaded from disk
// and a skills manager that rediscovers from that store.
type reloadFixture struct {
	app        *App
	store      *config.ConfigStore
	workDir    string
	skillsRoot string
}

// newReloadFixture isolates HOME/XDG so the developer's real config is never
// read. Callers must not use t.Parallel(): it calls t.Setenv.
func newReloadFixture(t *testing.T) *reloadFixture {
	t.Helper()

	isolated := t.TempDir()
	t.Setenv("HOME", isolated)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(isolated, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(isolated, ".local", "share"))
	t.Setenv("ANGELA_GLOBAL_CONFIG", filepath.Join(isolated, ".config", "angela"))
	t.Setenv("ANGELA_DISABLE_PROVIDER_AUTO_UPDATE", "true")

	fx := &reloadFixture{workDir: t.TempDir(), skillsRoot: t.TempDir()}
	writeReloadSkill(t, fx.skillsRoot, "alpha")
	fx.writeConfig(t, nil)

	store, err := config.Load(fx.workDir, t.TempDir(), false)
	require.NoError(t, err)
	fx.store = store

	source := func() skills.DiscoveryConfig {
		opts := store.Config().Options
		return skills.DiscoveryConfig{
			SkillsPaths:    opts.SkillsPaths,
			DisabledSkills: opts.DisabledSkills,
			WorkingDir:     store.WorkingDir(),
		}
	}
	mgr := skills.NewManager(nil, nil, nil, skills.WithDiscoverySource(source))
	t.Cleanup(mgr.Shutdown)
	_, err = mgr.Reload()
	require.NoError(t, err)

	fx.app = &App{config: store, Skills: mgr, globalCtx: t.Context()}
	return fx
}

func (fx *reloadFixture) writeConfig(t *testing.T, disabled []string) {
	t.Helper()
	options := map[string]any{"skills_paths": []string{fx.skillsRoot}}
	if len(disabled) > 0 {
		options["disabled_skills"] = disabled
	}
	data, err := json.Marshal(map[string]any{"options": options})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fx.workDir, "angela.json"), data, 0o600))
}

func writeReloadSkill(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body := "---\nname: " + name + "\ndescription: Skill " + name + " for reload tests.\n---\nDo a thing.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, skills.SkillFileName), []byte(body), 0o644))
}

func reloadActiveNames(app *App) []string {
	var names []string
	for _, s := range app.Skills.ActiveSkills() {
		names = append(names, s.Name)
	}
	return names
}

func TestApp_Reload_PicksUpConfigAndSkills(t *testing.T) {
	fx := newReloadFixture(t)
	require.Contains(t, reloadActiveNames(fx.app), "alpha")
	require.Empty(t, fx.store.Config().Options.DisabledSkills)

	writeReloadSkill(t, fx.skillsRoot, "beta")
	fx.writeConfig(t, []string{"alpha"})

	report, err := fx.app.Reload(t.Context())
	require.NoError(t, err)

	require.Equal(t, []string{"alpha"}, fx.store.Config().Options.DisabledSkills)
	require.True(t, report.SkillsReloaded)
	require.Equal(t, []string{"beta"}, report.Skills.Added)
	require.Equal(t, []string{"alpha"}, report.Skills.Removed)
	require.Contains(t, reloadActiveNames(fx.app), "beta")
	require.NotContains(t, reloadActiveNames(fx.app), "alpha")
	require.NotEmpty(t, report.ConfigPaths)

	summary := report.String()
	require.Contains(t, summary, "added beta")
	require.Contains(t, summary, "removed alpha")
	require.Contains(t, summary, "Not reloaded: permission rules")
}

func TestApp_Reload_InvalidConfigChangesNothing(t *testing.T) {
	fx := newReloadFixture(t)
	before := reloadActiveNames(fx.app)

	writeReloadSkill(t, fx.skillsRoot, "beta")
	require.NoError(t, os.WriteFile(
		filepath.Join(fx.workDir, "angela.json"), []byte(`{"options": {"skills_paths":`), 0o600))

	_, err := fx.app.Reload(t.Context())
	require.Error(t, err)

	require.Equal(t, before, reloadActiveNames(fx.app), "a failed reload must not touch skills")
	require.Empty(t, fx.store.Config().Options.DisabledSkills)
}

func TestApp_Reload_WithoutSkillsManager(t *testing.T) {
	fx := newReloadFixture(t)
	fx.app.Skills = nil

	report, err := fx.app.Reload(t.Context())
	require.NoError(t, err)
	require.False(t, report.SkillsReloaded)
	require.NotContains(t, report.String(), "Skills:")
}

func TestApp_ReloadForTool_ReturnsSummary(t *testing.T) {
	fx := newReloadFixture(t)

	summary, err := fx.app.reloadForTool(t.Context())
	require.NoError(t, err)
	require.Contains(t, summary, "Reloaded from disk.")

	require.NoError(t, os.WriteFile(
		filepath.Join(fx.workDir, "angela.json"), []byte(`not json`), 0o600))
	_, err = fx.app.reloadForTool(t.Context())
	require.Error(t, err)
}

func TestReloadReport_StringListsWarnings(t *testing.T) {
	t.Parallel()

	got := ReloadReport{Warnings: []string{"agents were not refreshed: boom"}}.String()
	require.Contains(t, got, "Warning: agents were not refreshed: boom")
	require.Contains(t, got, "MCP servers configured: 0")
}
