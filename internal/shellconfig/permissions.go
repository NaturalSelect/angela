package shellconfig

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/NaturalSelect/angela/internal/permission"
)

// handlePermissions implements the `permissions` builtin.
//
// Usage:
//
//	permissions allow <tool> [<tool> ...]
//	permissions deny <tool> [<tool> ...]
//	permissions rule <allow|ask|deny> [--tool <filter>] [--mode <mode>] [<pattern>]
//	permissions prompt <ask|deny>
//
// "allow" adds tools to the allow-list (tools that skip permission prompts).
// "deny" hides tools from the agent entirely (options.disabled_tools) — the
// inverse of allow. Adding the same tool twice is a no-op.
//
// "rule" adds a declarative rule. Rules are evaluated deny > ask > allow
// regardless of the order they are written in, so a deny always wins.
// "prompt" decides what happens when no rule settles a request.
//
// Precedence: deny wins. If a tool appears in both allow and deny, it is
// still removed from the agent's effective tool set via disabled_tools.
func handlePermissions(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	b := configBuilderFromCtx(ctx)
	if b == nil {
		return nil
	}
	if len(args) < 2 {
		return usage(stderr, "usage: permissions allow|deny|rule|prompt ...")
	}

	switch args[1] {
	case "allow":
		return permissionsAllow(b, args, stderr)
	case "deny":
		return permissionsDeny(b, args, stderr)
	case "rule":
		return permissionsRule(b, args, stderr)
	case "prompt":
		return permissionsPrompt(b, args, stderr)
	default:
		return usage(stderr, fmt.Sprintf("permissions: unknown subcommand %q (expected allow, deny, rule or prompt)", args[1]))
	}
}

func permissionsAllow(b *ConfigBuilder, args []string, stderr io.Writer) error {
	if len(args) < 3 {
		return usage(stderr, "usage: permissions allow <tool> [<tool> ...]")
	}
	perms := b.section("permissions")
	allowed, _ := perms["allowed_tools"].([]any)

	for _, tool := range args[2:] {
		if !containsAny(allowed, tool) {
			allowed = append(allowed, tool)
		}
	}
	perms["allowed_tools"] = allowed

	slog.Info("Permissions allowed in shell config", "tools", args[2:])
	return nil
}

// permissionsDeny hides tools from the agent by adding them to
// options.disabled_tools. It is the inverse of allow.
func permissionsDeny(b *ConfigBuilder, args []string, stderr io.Writer) error {
	if len(args) < 3 {
		return usage(stderr, "usage: permissions deny <tool> [<tool> ...]")
	}
	opts := b.section("options")
	disabled, _ := opts["disabled_tools"].([]any)

	for _, tool := range args[2:] {
		if !containsAny(disabled, tool) {
			disabled = append(disabled, tool)
		}
	}
	opts["disabled_tools"] = disabled

	slog.Info("Permissions denied in shell config", "tools", args[2:])
	return nil
}

// permissionsRule appends one declarative rule per pattern.
//
//	permissions rule deny --tool edit '**/.env' '**/id_rsa'
//	permissions rule allow --tool bash 'git status*'
//	permissions rule ask --tool network --domain example.com
func permissionsRule(b *ConfigBuilder, args []string, stderr io.Writer) error {
	const usageLine = "usage: permissions rule allow|ask|deny [--tool <filter>] [--path|--free|--domain] [<pattern> ...]"
	if len(args) < 3 {
		return usage(stderr, usageLine)
	}

	action := args[2]
	if _, ok := permission.ParseRuleAction(action); !ok {
		return usage(stderr, fmt.Sprintf("permissions rule: unknown action %q (expected allow, ask or deny)", action))
	}

	var tool, mode string
	var patterns []string

	for i := 3; i < len(args); i++ {
		switch arg := args[i]; arg {
		case "--tool":
			if i+1 >= len(args) {
				return usage(stderr, "permissions rule: --tool requires a value")
			}
			tool = args[i+1]
			i++
		case "--path", "--free", "--domain":
			if mode != "" {
				return usage(stderr, "permissions rule: pick one of --path, --free or --domain")
			}
			mode = strings.TrimPrefix(arg, "--")
		default:
			if strings.HasPrefix(arg, "--") {
				return usage(stderr, fmt.Sprintf("permissions rule: unknown flag %s", arg))
			}
			patterns = append(patterns, arg)
		}
	}

	// A rule with no pattern matches every access the tool filter
	// admits, which is what `permissions rule deny --tool network` means.
	if len(patterns) == 0 {
		patterns = []string{""}
	}

	perms := b.section("permissions")
	rules, _ := perms["rules"].([]any)
	for _, pattern := range patterns {
		rule := map[string]any{"action": action}
		if tool != "" {
			rule["tool"] = tool
		}
		if mode != "" {
			rule["mode"] = mode
		}
		if pattern != "" {
			rule["pattern"] = pattern
		}
		rules = append(rules, rule)
	}
	perms["rules"] = rules

	slog.Info("Permission rules added in shell config",
		"action", action, "tool", tool, "patterns", patterns)
	return nil
}

// permissionsPrompt sets what happens when no rule settles a request.
func permissionsPrompt(b *ConfigBuilder, args []string, stderr io.Writer) error {
	if len(args) != 3 {
		return usage(stderr, "usage: permissions prompt ask|deny")
	}
	policy, ok := permission.ParsePromptPolicy(args[2])
	if !ok || policy == permission.PromptAllow {
		return usage(stderr, fmt.Sprintf("permissions prompt: unknown policy %q (expected ask or deny)", args[2]))
	}

	b.section("permissions")["prompt"] = args[2]

	slog.Info("Permission prompt policy set in shell config", "policy", args[2])
	return nil
}

// containsAny reports whether the slice already holds the given string value.
func containsAny(s []any, v string) bool {
	for _, item := range s {
		if str, ok := item.(string); ok && str == v {
			return true
		}
	}
	return false
}
