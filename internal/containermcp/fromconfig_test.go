package containermcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/env"
	"github.com/stretchr/testify/require"
)

func resolvers(vars map[string]string) (field, argsEnv config.VariableResolver) {
	e := env.NewFromMap(vars)
	return config.NewEnvOnlyVariableResolver(e), config.NewShellVariableResolver(e)
}

func TestFromConfig_FieldsNeverRunCommands(t *testing.T) {
	t.Parallel()

	field, argsEnv := resolvers(map[string]string{"TAG": "1.2", "TOKEN": "s3cret"})
	m := config.MCPConfig{
		Type:    config.MCPContainer,
		Image:   "img:$TAG-$(echo pwned)",
		Args:    []string{"--tz", "$(echo UTC)"},
		Env:     map[string]string{"TOKEN": "$TOKEN"},
		Network: "none",
		Workdir: "/work",
	}

	g, err := FromConfig(t.Context(), "time", m, t.TempDir(), field, argsEnv)
	require.NoError(t, err)

	require.Equal(t, "time", g.Server)
	require.Equal(t, "img:1.2-$(echo pwned)", g.Main.Image)
	require.Equal(t, []string{"--tz", "UTC"}, g.Main.Cmd)
	require.Equal(t, map[string]string{"TOKEN": "s3cret"}, g.Main.Env)
	require.Equal(t, "none", g.Network)
	require.Equal(t, "/work", g.Main.WorkingDir)
	require.Equal(t, PullMissing, g.Main.Pull)
	require.Empty(t, g.Deps)
}

func TestFromConfig_PullPolicy(t *testing.T) {
	t.Parallel()

	field, argsEnv := resolvers(nil)
	g, err := FromConfig(t.Context(), "s", config.MCPConfig{Image: "img", Pull: "never"}, "", field, argsEnv)
	require.NoError(t, err)
	require.Equal(t, PullNever, g.Main.Pull)

	_, err = FromConfig(t.Context(), "s", config.MCPConfig{Image: "img", Pull: "sometimes"}, "", field, argsEnv)
	require.ErrorContains(t, err, "pull must be")
}

func TestFromConfig_Volumes(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(workDir, "data"), 0o755))
	field, argsEnv := resolvers(map[string]string{"SUB": "data"})

	g, err := FromConfig(t.Context(), "s", config.MCPConfig{
		Image:   "img",
		Volumes: []string{"./$SUB:/data:ro", "cache:/cache", "./data:/rw:z"},
	}, workDir, field, argsEnv)
	require.NoError(t, err)

	require.Equal(t, []Mount{
		{Type: MountBind, Source: filepath.Join(workDir, "data"), Target: "/data", ReadOnly: true},
		{Type: MountVolume, Source: "cache", Target: "/cache"},
		{Type: MountBind, Source: filepath.Join(workDir, "data"), Target: "/rw", Relabel: "z"},
	}, g.Main.Mounts)
}

func TestFromConfig_VolumeErrors(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	field, argsEnv := resolvers(nil)

	tests := []struct {
		name    string
		volume  string
		workDir string
		wantErr string
	}{
		{"missing target", "./data", workDir, "must be source:target"},
		{"too many parts", "a:/b:ro:x", workDir, "must be source:target"},
		{"empty source", ":/data", workDir, "empty source"},
		{"unknown option", "cache:/data:fast", workDir, "unknown option"},
		{"both relabels", "cache:/data:z,Z", workDir, "both z and Z"},
		{"missing bind source", "./nope:/data", workDir, "does not exist"},
		{"relative without project dir", "./data:/data", "", "project directory is unknown"},
		{"relative target", "cache:data", workDir, "absolute path"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := FromConfig(t.Context(), "s", config.MCPConfig{Image: "img", Volumes: []string{tc.volume}}, tc.workDir, field, argsEnv)
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestFromConfig_RejectsForeignFields(t *testing.T) {
	t.Parallel()

	field, argsEnv := resolvers(nil)
	tests := []struct {
		name string
		m    config.MCPConfig
		want string
	}{
		{"command", config.MCPConfig{Image: "img", Command: "docker"}, "'command'"},
		{"url", config.MCPConfig{Image: "img", URL: "https://x"}, "'url'"},
		{"headers", config.MCPConfig{Image: "img", Headers: map[string]string{"a": "b"}}, "'headers'"},
		{"oauth", config.MCPConfig{Image: "img", OAuth: true}, "oauth"},
		{"oauth client", config.MCPConfig{Image: "img", OAuthClientID: "x"}, "oauth"},
		{"no image", config.MCPConfig{}, "has no image"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := FromConfig(t.Context(), "s", tc.m, "", field, argsEnv)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestFromConfig_ResolverErrorsNameTheField(t *testing.T) {
	t.Parallel()

	_, argsEnv := resolvers(nil)
	_, err := FromConfig(t.Context(), "s", config.MCPConfig{Image: "img", Args: []string{"$(false)"}}, "", config.IdentityResolver(), argsEnv)
	require.ErrorContains(t, err, "arg 0")
}

func composeConfigFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "compose.yaml"), `
services:
  mcp:
    image: example/mcp
    depends_on: [db]
  db:
    image: example/db
`)
	return dir
}

func TestFromConfig_Compose(t *testing.T) {
	t.Parallel()

	dir := composeConfigFixture(t)
	field, argsEnv := resolvers(map[string]string{"SVC": "mcp"})

	g, err := FromConfig(t.Context(), "stack", config.MCPConfig{
		Type: config.MCPContainer, Compose: "./compose.yaml", Service: "$SVC",
	}, dir, field, argsEnv)
	require.NoError(t, err)
	require.Equal(t, "stack", g.Server)
	require.Equal(t, "mcp", g.Main.Name)
	require.Len(t, g.Deps, 1)
	require.Equal(t, PullMissing, g.Main.Pull, "a service without pull_policy pulls a missing image")
}

func TestFromConfig_ComposePullOverridesEveryService(t *testing.T) {
	t.Parallel()

	dir := composeConfigFixture(t)
	field, argsEnv := resolvers(nil)

	g, err := FromConfig(t.Context(), "stack", config.MCPConfig{Compose: "compose.yaml", Service: "mcp", Pull: "never"}, dir, field, argsEnv)
	require.NoError(t, err)
	require.Equal(t, PullNever, g.Main.Pull)
	require.Equal(t, PullNever, g.Deps[0].Pull)

	_, err = FromConfig(t.Context(), "stack", config.MCPConfig{Compose: "compose.yaml", Service: "mcp", Pull: "sometimes"}, dir, field, argsEnv)
	require.ErrorContains(t, err, "pull must be")
}

func TestFromConfig_ComposeConflicts(t *testing.T) {
	t.Parallel()

	dir := composeConfigFixture(t)
	field, argsEnv := resolvers(nil)
	base := config.MCPConfig{Compose: "compose.yaml", Service: "mcp"}
	with := func(f func(*config.MCPConfig)) config.MCPConfig {
		m := base
		f(&m)
		return m
	}
	tests := []struct {
		name string
		m    config.MCPConfig
		want string
	}{
		{"image and compose", with(func(m *config.MCPConfig) { m.Image = "img" }), "both 'image' and 'compose'"},
		{"service alone", config.MCPConfig{Service: "mcp"}, "'service' without 'compose'"},
		{"compose alone", config.MCPConfig{Compose: "compose.yaml"}, "'compose' without 'service'"},
		{"args", with(func(m *config.MCPConfig) { m.Args = []string{"x"} }), "'args'"},
		{"env", with(func(m *config.MCPConfig) { m.Env = map[string]string{"A": "b"} }), "'env'"},
		{"volumes", with(func(m *config.MCPConfig) { m.Volumes = []string{"a:/b"} }), "'volumes'"},
		{"network", with(func(m *config.MCPConfig) { m.Network = "none" }), "'network'"},
		{"workdir", with(func(m *config.MCPConfig) { m.Workdir = "/w" }), "'workdir'"},
		{"unknown service", with(func(m *config.MCPConfig) { m.Service = "nope" }), `no service "nope"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := FromConfig(t.Context(), "stack", tc.m, dir, field, argsEnv)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestSplitVolume(t *testing.T) {
	t.Parallel()

	require.Equal(t, []string{"a", "/b", "ro"}, splitVolume("a:/b:ro", false))
	require.Equal(t, []string{"a:/b", "ro"}, splitVolume("a:/b:ro", true), "a drive letter wins on Windows")
	require.Equal(t, []string{`C:\data`, "/data"}, splitVolume(`C:\data:/data`, true))
	require.Equal(t, []string{"C:/data", "/data", "ro"}, splitVolume("C:/data:/data:ro", true))
	require.Equal(t, []string{"C", `\data`, "/data"}, splitVolume(`C:\data:/data`, false))
	require.Equal(t, []string{"x"}, splitVolume("x", false))
}
