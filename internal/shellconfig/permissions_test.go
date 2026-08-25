package shellconfig

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/stretchr/testify/require"
)

func loadPermissions(t *testing.T, script string) map[string]any {
	t.Helper()

	path := filepath.Join(t.TempDir(), "angelarc")
	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(script))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	perms, _ := result["permissions"].(map[string]any)
	return perms
}

func TestPermissions_AllowAndDeny(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "angelarc")
	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(
		"permissions allow bash view\npermissions deny sourcegraph"))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(jsonBytes, &result))

	perms := result["permissions"].(map[string]any)
	require.Equal(t, []any{"bash", "view"}, perms["allowed_tools"])

	opts := result["options"].(map[string]any)
	require.Equal(t, []any{"sourcegraph"}, opts["disabled_tools"])
}

func TestPermissions_Rule(t *testing.T) {
	t.Parallel()

	perms := loadPermissions(t, `permissions rule deny --tool edit '**/.env'
permissions rule allow --tool bash 'git status*'
permissions rule ask --tool network --domain example.com
permissions rule deny`)

	require.Equal(t, []any{
		map[string]any{"action": "deny", "tool": "edit", "pattern": "**/.env"},
		map[string]any{"action": "allow", "tool": "bash", "pattern": "git status*"},
		map[string]any{"action": "ask", "tool": "network", "mode": "domain", "pattern": "example.com"},
		map[string]any{"action": "deny"},
	}, perms["rules"])
}

// TestPermissions_RuleFansOutPatterns pins that listing several patterns
// writes one rule each. Denying a set of secret files in one line is the
// common case, and silently keeping only the first would leave the rest
// wide open.
func TestPermissions_RuleFansOutPatterns(t *testing.T) {
	t.Parallel()

	perms := loadPermissions(t,
		"permissions rule deny --tool read '**/.env' '**/id_rsa' '**/*.pem'")

	require.Equal(t, []any{
		map[string]any{"action": "deny", "tool": "read", "pattern": "**/.env"},
		map[string]any{"action": "deny", "tool": "read", "pattern": "**/id_rsa"},
		map[string]any{"action": "deny", "tool": "read", "pattern": "**/*.pem"},
	}, perms["rules"])
}

func TestPermissions_Prompt(t *testing.T) {
	t.Parallel()

	perms := loadPermissions(t, "permissions prompt deny")
	require.Equal(t, "deny", perms["prompt"])
}

// TestPermissions_RuleRejectsBadInput pins that a typo in a rule is
// reported rather than silently dropped: a rule the user thought they
// wrote is the difference between a gate and no gate.
func TestPermissions_RuleRejectsBadInput(t *testing.T) {
	t.Parallel()

	scripts := map[string]string{
		"unknown action":    "permissions rule maybe --tool edit '*'",
		"unknown flag":      "permissions rule deny --scope repo '*'",
		"stale mode flag":   "permissions rule deny --mode domain '*'",
		"two modes":         "permissions rule deny --path --free '*'",
		"missing value":     "permissions rule deny --tool",
		"missing action":    "permissions rule",
		"unknown prompt":    "permissions prompt sometimes",
		"prompt allow":      "permissions prompt allow",
		"unknown subcmd":    "permissions maybe bash",
		"prompt extra args": "permissions prompt ask deny",
	}

	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "angelarc")
			_, err := LoadShellConfig(t.Context(), path, []byte(script))
			require.Error(t, err, "%q must be rejected", script)
		})
	}
}

// TestPermissions_RulesReachThePolicy pins the whole path: what the
// angelarc writes must survive JSON decoding into the permission types
// and compile into a policy that actually denies.
func TestPermissions_RulesReachThePolicy(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "angelarc")
	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(
		"permissions rule deny --tool edit '**/.env'\npermissions prompt deny"))
	require.NoError(t, err)

	// Mirrors config.Permissions, which cannot be imported here because
	// config depends on this package.
	var decoded struct {
		Permissions struct {
			Rules        []permission.Rule `json:"rules"`
			AllowedTools []string          `json:"allowed_tools"`
			Prompt       string            `json:"prompt"`
		} `json:"permissions"`
	}
	require.NoError(t, json.Unmarshal(jsonBytes, &decoded))
	require.Equal(t, "deny", decoded.Permissions.Prompt)
	require.Len(t, decoded.Permissions.Rules, 1)

	prompt, ok := permission.ParsePromptPolicy(decoded.Permissions.Prompt)
	require.True(t, ok)
	policy, err := permission.CompilePolicy(
		decoded.Permissions.Rules, decoded.Permissions.AllowedTools, prompt)
	require.NoError(t, err)

	verdict := policy.Evaluate(permission.Access{
		Tool:   "edit",
		Action: permission.ActionEdit,
		Path:   "/work/.env",
	}, "/work")
	require.True(t, verdict.Matched)
	require.Equal(t, permission.RuleDeny, verdict.Action)
}

// TestPermissions_ReadDenyCoversBothRoutes pins the promise the docs
// make about `permissions rule deny --tool read`: reading a secret is
// denied whether the model reaches for the view tool or types `cat`.
// The two used to be separate worlds, and the shell route was the one
// left open.
func TestPermissions_ReadDenyCoversBothRoutes(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "angelarc")
	jsonBytes, err := LoadShellConfig(t.Context(), path, []byte(
		"permissions rule deny --tool read '**/.env'"))
	require.NoError(t, err)

	var decoded struct {
		Permissions struct {
			Rules []permission.Rule `json:"rules"`
		} `json:"permissions"`
	}
	require.NoError(t, json.Unmarshal(jsonBytes, &decoded))

	policy, err := permission.CompilePolicy(
		decoded.Permissions.Rules, nil, permission.PromptAsk)
	require.NoError(t, err)

	routes := map[string]permission.Access{
		"the view tool": {
			Tool: "view", Action: permission.ActionRead, Path: "/work/.env",
		},
		"cat in bash": {
			Tool: "bash", Action: permission.ActionExecute,
			Command: "cat .env", Path: "/work",
		},
		"a chain ending in cat": {
			Tool: "bash", Action: permission.ActionExecute,
			Command: "ls && cat .env", Path: "/work",
		},
	}

	for name, access := range routes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			verdict := policy.Evaluate(access, "/work")
			require.True(t, verdict.Matched, "%s must be judged", name)
			require.Equal(t, permission.RuleDeny, verdict.Action)
		})
	}
}
