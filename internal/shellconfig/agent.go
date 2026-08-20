package shellconfig

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
)

// PrimaryAgent is the ID of the agent that runs as primary and is the
// root every other agent's inherited permissions expand to. It must
// stay in sync with config.AgentCoder, which cannot be imported here:
// config imports shellconfig to run angelarc.
const PrimaryAgent = "coder"

// handleAgent implements the `agent` builtin.
//
// Usage:
//
//	agent add <name> [--description DESC] [--mode primary|subagent]
//	    [--model NAME] [--prompt TEXT] [--temperature T]
//	    [--tool TOOL ...] [--tools all|inherited]
//	    [--disable-tool TOOL ...] [--mcp all|inherited]
//	    [--mcp-scope JSON] [--disabled true|false]
//	    [--hidden true|false] [--max-tokens N]
//	agent remove <name>   (alias: rm)
func handleAgent(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	b := configBuilderFromCtx(ctx)
	if b == nil {
		return nil
	}
	if len(args) < 2 {
		return usage(stderr, "usage: agent add <name> [flags] | agent remove <name>")
	}

	switch args[1] {
	case "add":
		return agentAdd(b, args, stderr)
	case "remove", "rm":
		return agentRemove(b, args, stderr)
	default:
		return usage(stderr, fmt.Sprintf("agent: unknown subcommand %q (expected add or remove)", args[1]))
	}
}

// agentSetLiteral validates the two words accepted by the tri-state
// permission flags. The config package owns the canonical validation,
// but it imports shellconfig to run angelarc, so the literals are
// spelled out again here rather than imported.
func agentSetLiteral(flag string) func(any) error {
	return func(v any) error {
		s, _ := v.(string)
		switch s {
		case "all", "inherited":
			return nil
		default:
			return fmt.Errorf("%s must be all or inherited; got %q", flag, s)
		}
	}
}

// agentAddFlags is the declarative flag surface for `agent add`.
var agentAddFlags = []flagSpec{
	{name: "--description", jsonKey: "description", kind: flagString, op: opSet},
	{name: "--mode", jsonKey: "mode", kind: flagString, op: opSet, validate: func(v any) error {
		s, _ := v.(string)
		switch s {
		case "primary", "subagent":
			return nil
		default:
			return fmt.Errorf("mode must be primary or subagent; got %q", s)
		}
	}},
	{name: "--model", jsonKey: "model", kind: flagString, op: opSet},
	{name: "--variant", jsonKey: "variant", kind: flagString, op: opSet},
	{name: "--prompt", jsonKey: "prompt", kind: flagString, op: opSet},
	{name: "--temperature", jsonKey: "temperature", kind: flagFloat, op: opSet, validate: func(v any) error {
		f, _ := v.(float64)
		// NaN fails both comparisons of a plain range check, so it
		// has to be rejected explicitly.
		if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1 {
			return fmt.Errorf("temperature must be between 0 and 1; got %v", f)
		}
		return nil
	}},
	{name: "--tool", jsonKey: "allowed_tools", kind: flagString, op: opAppend},
	{name: "--tools", jsonKey: "allowed_tools", kind: flagString, op: opSet, validate: agentSetLiteral("tools")},
	{name: "--disable-tool", jsonKey: "disabled_tools", kind: flagString, op: opAppend},
	{name: "--mcp", jsonKey: "allowed_mcp", kind: flagString, op: opSet, validate: agentSetLiteral("mcp")},
	{name: "--mcp-scope", jsonKey: "allowed_mcp", kind: flagJSONObject, op: opSet},
	{name: "--disabled", jsonKey: "disabled", kind: flagBool, op: opSet},
	{name: "--hidden", jsonKey: "hidden", kind: flagBool, op: opSet},
	{name: "--max-tokens", jsonKey: "max_tokens", kind: flagInt, op: opSet},
}

func agentAdd(b *ConfigBuilder, args []string, stderr io.Writer) error {
	if len(args) < 3 {
		return usage(stderr, "usage: agent add <name> [--description DESC] [--mode primary|subagent] [--model NAME] [--variant NAME] [--prompt TEXT] [--temperature T] [--tool TOOL ...] [--tools all|inherited] [--disable-tool TOOL ...] [--mcp all|inherited] [--mcp-scope JSON] [--disabled true|false] [--hidden true|false] [--max-tokens N]")
	}
	name := args[2]
	slog.Info("Agent defined in shell config", "name", name)
	m := childMap(b.section("agents"), name)

	if err := applyFlags(agentAddFlags, args, 3, m, "agent add", stderr); err != nil {
		return err
	}

	slog.Debug("Agent recorded", "name", name)
	return nil
}

// agentRemove writes a disable tombstone rather than dropping the key
// from this builder. Removing the key would only undo definitions made
// earlier in the same script; agents also come from built-in defaults
// and markdown files, which a lower-priority layer cannot delete. A
// tombstone is an override like any other, so it suppresses those too.
func agentRemove(b *ConfigBuilder, args []string, stderr io.Writer) error {
	if len(args) < 3 {
		return usage(stderr, "usage: agent remove <name>")
	}
	name := args[2]
	if name == PrimaryAgent {
		return usage(stderr, "agent remove: the coder agent cannot be removed")
	}
	m := childMap(b.section("agents"), name)
	clear(m)
	m["disabled"] = true
	slog.Info("Agent disabled in shell config", "name", name)
	return nil
}
