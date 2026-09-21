package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/home"
	"github.com/stretchr/testify/require"
)

// writeGlobalConfig writes doc as the global config file (pointed at by
// ANGELA_GLOBAL_CONFIG) and returns its path, which trustedPermissionConfig
// recognizes as the trusted layer.
func writeGlobalConfig(t *testing.T, doc string) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", dir)
	path := GlobalConfig()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o644))
	return path
}

// writeProjectConfig writes doc as an ordinary, untrusted project-level
// angela.json in a fresh temp directory.
func writeProjectConfig(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "angela.json")
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o644))
	return path
}

// TestExpandPermissionRulePatterns_GlobalGetsFullShellExpansion pins that
// the global config -- always user-authored, never auto-loaded from a
// cloned repository -- may use the full shell resolver in patterns,
// including $VAR substitution and $(command) substitution.
func TestExpandPermissionRulePatterns_GlobalGetsFullShellExpansion(t *testing.T) {
	// NOTE: t.Setenv (direct or via writeGlobalConfig) forbids t.Parallel.
	t.Setenv("ANGELA_TEST_NOTES_DIR", "notes")

	path := writeGlobalConfig(t, `{"permissions": {"rules": [
		{"action": "allow", "tool": "read", "pattern": "$ANGELA_TEST_NOTES_DIR/**"},
		{"action": "allow", "tool": "read", "pattern": "$(echo -n allowed)/**"}
	]}}`)

	cfg, _, _, err := loadFromConfigPaths(context.Background(), []string{path})
	require.NoError(t, err)
	require.NotNil(t, cfg.Permissions)
	require.Len(t, cfg.Permissions.Rules, 2)
	require.Equal(t, "notes/**", cfg.Permissions.Rules[0].Pattern, "$VAR must resolve in the global config")
	require.Equal(t, "allowed/**", cfg.Permissions.Rules[1].Pattern, "$(command) must run for the trusted global config")
}

// TestExpandPermissionRulePatterns_GlobalExpandsTilde pins that a leading
// "~" in a global-config pattern resolves to the real home directory,
// matching the existing home.Long precedent used for agent/skills/MCP
// paths elsewhere in config loading.
func TestExpandPermissionRulePatterns_GlobalExpandsTilde(t *testing.T) {
	// NOTE: writeGlobalConfig calls t.Setenv, which forbids t.Parallel.
	path := writeGlobalConfig(t, `{"permissions": {"rules": [
		{"action": "allow", "tool": "read", "pattern": "~/notes/**"}
	]}}`)

	cfg, _, _, err := loadFromConfigPaths(context.Background(), []string{path})
	require.NoError(t, err)
	require.Len(t, cfg.Permissions.Rules, 1)
	require.Equal(t, home.Dir()+"/notes/**", cfg.Permissions.Rules[0].Pattern)
}

// TestExpandPermissionRulePatterns_GlobalInvalidCommandSubstitutionFails
// pins that a malformed $(...) in the trusted global config is a hard
// load error rather than being silently left as a literal or ignored.
func TestExpandPermissionRulePatterns_GlobalInvalidCommandSubstitutionFails(t *testing.T) {
	// NOTE: writeGlobalConfig calls t.Setenv, which forbids t.Parallel.
	path := writeGlobalConfig(t, `{"permissions": {"rules": [
		{"action": "allow", "tool": "read", "pattern": "$("}
	]}}`)

	_, _, _, err := loadFromConfigPaths(context.Background(), []string{path})
	require.Error(t, err)
	require.Contains(t, err.Error(), "permission rule 0")
}

// TestExpandPermissionRulePatterns_ProjectOnlyReadsEnvVar pins the core
// safety property: a project-level angela.json (auto-loaded even from a
// freshly cloned, untrusted repository, per lookupConfigs) can only have
// its patterns read an existing environment variable's value. It must
// never gain command-execution power merely by being loaded.
func TestExpandPermissionRulePatterns_ProjectOnlyReadsEnvVar(t *testing.T) {
	// NOTE: t.Setenv forbids t.Parallel.
	t.Setenv("PROJECT_TEST_VAR", "secretpath")

	path := writeProjectConfig(t, `{"permissions": {"rules": [
		{"action": "allow", "tool": "read", "pattern": "$PROJECT_TEST_VAR/**"},
		{"action": "allow", "tool": "read", "pattern": "$(echo pwned)/**"},
		{"action": "allow", "tool": "read", "pattern": "$UNSET_PROJECT_TEST_VAR/**"}
	]}}`)

	cfg, _, _, err := loadFromConfigPaths(context.Background(), []string{path})
	require.NoError(t, err)
	require.Len(t, cfg.Permissions.Rules, 3)
	require.Equal(t, "secretpath/**", cfg.Permissions.Rules[0].Pattern, "plain $VAR must still resolve")
	require.Equal(t, "$(echo pwned)/**", cfg.Permissions.Rules[1].Pattern,
		"command substitution must never execute for an untrusted project config")
	require.Equal(t, "/**", cfg.Permissions.Rules[2].Pattern, "an unset var reads as empty, matching os.ExpandEnv")
}

// TestExpandPermissionRulePatterns_ProjectExpandsTilde pins that tilde
// expansion is not gated by trust: resolving "~" is no riskier than
// reading $HOME, which project-level patterns are already allowed to do.
func TestExpandPermissionRulePatterns_ProjectExpandsTilde(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, `{"permissions": {"rules": [
		{"action": "allow", "tool": "read", "pattern": "~/notes/**"}
	]}}`)

	cfg, _, _, err := loadFromConfigPaths(context.Background(), []string{path})
	require.NoError(t, err)
	require.Len(t, cfg.Permissions.Rules, 1)
	require.Equal(t, home.Dir()+"/notes/**", cfg.Permissions.Rules[0].Pattern)
}

// TestExpandPermissionRulePatterns_LeavesOrdinaryPatternsUntouched pins
// that rules without "$" or "~" -- the overwhelming common case -- and
// the other rule fields survive loading byte-for-byte.
func TestExpandPermissionRulePatterns_LeavesOrdinaryPatternsUntouched(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, `{"permissions": {"rules": [
		{"action": "deny", "tool": "read", "pattern": "**/.env", "mode": "path"},
		{"action": "allow", "tool": "bash", "pattern": "git status*"}
	]}}`)

	cfg, _, _, err := loadFromConfigPaths(context.Background(), []string{path})
	require.NoError(t, err)
	require.Len(t, cfg.Permissions.Rules, 2)
	require.Equal(t, "**/.env", cfg.Permissions.Rules[0].Pattern)
	require.Equal(t, "git status*", cfg.Permissions.Rules[1].Pattern)
}
