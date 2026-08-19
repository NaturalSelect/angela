package shellconfig

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strings"
)

// handleModel implements the `model` builtin.
//
// Usage:
//
//	model add <provider>/<id> [--name NAME] [--context-window N]
//	    [--default-max-tokens N] [--can-reason true|false]
//	    [--supports-images true|false] [--price-input F]
//	    [--price-output F] [--price-cache-create F]
//	    [--price-cache-hit F] [--reasoning-effort low|medium|high]
//	    [--reasoning-level LEVEL ...]
//	model remove <provider>/<id>   (alias: rm)
//	model <name> [<provider>/<id>] [--think] [--reasoning-effort L]
//	    [--max-tokens N] [--temperature F] [--top-p F] [--top-k N]
//	    [--frequency-penalty F] [--presence-penalty F]
//	    [--provider-options JSON]
//	model <name> variant <variant> [same parameter flags]
//
// "add" registers a model on an existing provider (the provider must have
// been declared with `provider add` first). "remove" removes it. Any other
// word names a model config ("main" and "chore" ship as seeds) and sets its
// selection, or prints the current selection as <provider>/<id> when given
// no argument.
func handleModel(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	b := configBuilderFromCtx(ctx)
	if b == nil {
		return nil
	}
	if len(args) < 2 {
		return usage(stderr, "usage: model add|remove <provider>/<id> | model <name> [<provider>/<id>]")
	}

	switch args[1] {
	case "add":
		return modelAdd(b, args, stderr)
	case "remove", "rm":
		return modelRemove(b, args, stderr)
	default:
		return modelSelect(b, args, stdout, stderr)
	}
}

// splitProviderModel splits "provider/id" on the first slash. Model ids may
// themselves contain slashes, so only the first separates provider from id.
func splitProviderModel(s string) (provider, id string, ok bool) {
	provider, id, found := strings.Cut(s, "/")
	if !found || provider == "" || id == "" {
		return "", "", false
	}
	return provider, id, true
}

// modelAddFlags is the declarative flag surface for `model add`.
var modelAddFlags = []flagSpec{
	{name: "--name", jsonKey: "name", kind: flagString, op: opSet},
	{name: "--context-window", jsonKey: "context_window", kind: flagInt, op: opSet},
	{name: "--default-max-tokens", jsonKey: "default_max_tokens", kind: flagInt, op: opSet},
	{name: "--can-reason", jsonKey: "can_reason", kind: flagBool, op: opSet},
	{name: "--supports-images", jsonKey: "supports_attachments", kind: flagBool, op: opSet},
	{name: "--price-input", jsonKey: "cost_per_1m_in", kind: flagFloat, op: opSet},
	{name: "--price-output", jsonKey: "cost_per_1m_out", kind: flagFloat, op: opSet},
	{name: "--price-cache-create", jsonKey: "cost_per_1m_out_cached", kind: flagFloat, op: opSet},
	{name: "--price-cache-hit", jsonKey: "cost_per_1m_in_cached", kind: flagFloat, op: opSet},
	{name: "--reasoning-effort", jsonKey: "default_reasoning_effort", kind: flagString, op: opSet},
	// Repeatable: one level per flag occurrence. A model's declared
	// levels gate whether --reasoning-effort (here or on `model
	// large`/`model small`) ever takes effect, so a custom model needs
	// this set before any reasoning effort request reaches the provider.
	{name: "--reasoning-level", jsonKey: "reasoning_levels", kind: flagString, op: opAppend},
}

func modelAdd(b *ConfigBuilder, args []string, stderr io.Writer) error {
	if len(args) < 3 {
		return usage(stderr, "usage: model add <provider>/<id> [--name NAME] [--context-window N] [--default-max-tokens N] [--can-reason true|false] [--supports-images true|false] [--price-input F] [--price-output F] [--price-cache-create F] [--price-cache-hit F] [--reasoning-effort low|medium|high] [--reasoning-level LEVEL ...]")
	}
	provider, id, ok := splitProviderModel(args[2])
	if !ok {
		return usage(stderr, fmt.Sprintf("model add: expected <provider>/<id>, got %q", args[2]))
	}

	providers := b.section("providers")
	if _, exists := providers[provider]; !exists {
		return usage(stderr, fmt.Sprintf("model add: provider %q does not exist (declare it with `provider add %s` first)", provider, provider))
	}

	model := map[string]any{"id": id}
	if err := applyFlags(modelAddFlags, args, 3, model, "model add", stderr); err != nil {
		return err
	}

	p := childMap(providers, provider)
	// Re-adding a model id replaces the existing entry, matching the
	// update-in-place behavior of `provider add` and `lsp add`.
	modelsArr, _ := p["models"].([]any)
	kept := make([]any, 0, len(modelsArr)+1)
	for _, item := range modelsArr {
		if m, ok := item.(map[string]any); ok && m["id"] == id {
			continue
		}
		kept = append(kept, item)
	}
	p["models"] = append(kept, model)

	slog.Info("Model added in shell config", "provider", provider, "model", id)
	return nil
}

func modelRemove(b *ConfigBuilder, args []string, stderr io.Writer) error {
	if len(args) < 3 {
		return usage(stderr, "usage: model remove <provider>/<id>")
	}
	provider, id, ok := splitProviderModel(args[2])
	if !ok {
		return usage(stderr, fmt.Sprintf("model remove: expected <provider>/<id>, got %q", args[2]))
	}

	providers := b.section("providers")
	p, exists := providers[provider].(map[string]any)
	if !exists {
		return nil
	}
	modelsArr, _ := p["models"].([]any)
	kept := make([]any, 0, len(modelsArr))
	for _, item := range modelsArr {
		m, ok := item.(map[string]any)
		if ok && m["id"] == id {
			continue
		}
		kept = append(kept, item)
	}
	p["models"] = kept

	slog.Info("Model removed in shell config", "provider", provider, "model", id)
	return nil
}

// modelSelectFlags is the declarative flag surface for `model <name>`.
var modelSelectFlags = []flagSpec{
	{name: "--think", jsonKey: "think", kind: flagBoolTrue, op: opSet},
	{name: "--reasoning-effort", jsonKey: "reasoning_effort", kind: flagString, op: opSet},
	{name: "--max-tokens", jsonKey: "max_tokens", kind: flagInt, op: opSet, validate: nonNegative("--max-tokens")},
	{name: "--temperature", jsonKey: "temperature", kind: flagFloat, op: opSet, validate: unitRange("--temperature")},
	{name: "--top-p", jsonKey: "top_p", kind: flagFloat, op: opSet, validate: unitRange("--top-p")},
	{name: "--top-k", jsonKey: "top_k", kind: flagInt, op: opSet},
	{name: "--frequency-penalty", jsonKey: "frequency_penalty", kind: flagFloat, op: opSet, validate: finite("--frequency-penalty")},
	{name: "--presence-penalty", jsonKey: "presence_penalty", kind: flagFloat, op: opSet, validate: finite("--presence-penalty")},
	{name: "--provider-options", child: "provider_options", kind: flagJSONObject, op: opMergeChild},
}

// unitRange holds a float flag to [0,1]. NaN fails both comparisons of
// a plain range check, so it has to be rejected explicitly.
func unitRange(flag string) func(any) error {
	return func(v any) error {
		f, _ := v.(float64)
		if math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 1 {
			return fmt.Errorf("%s expects a value between 0 and 1, got %v", flag, f)
		}
		return nil
	}
}

// finite rejects NaN and the infinities, which have no JSON form.
func finite(flag string) func(any) error {
	return func(v any) error {
		f, _ := v.(float64)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("%s expects a finite number, got %v", flag, f)
		}
		return nil
	}
}

func nonNegative(flag string) func(any) error {
	return func(v any) error {
		n, _ := v.(int64)
		if n < 0 {
			return fmt.Errorf("%s expects a non-negative value, got %d", flag, n)
		}
		return nil
	}
}

func modelSelect(b *ConfigBuilder, args []string, stdout, stderr io.Writer) error {
	slot := args[1]

	// No argument: print the current selection as <provider>/<id>.
	if len(args) == 2 {
		if models, ok := b.root["models"].(map[string]any); ok {
			if sel, ok := models[slot].(map[string]any); ok {
				provider, _ := sel["provider"].(string)
				id, _ := sel["model"].(string)
				if provider != "" && id != "" {
					fmt.Fprintln(stdout, provider+"/"+id)
				}
			}
		}
		return nil
	}

	if args[2] == "variant" {
		return modelVariant(b, args, stderr)
	}

	provider, id, ok := splitProviderModel(args[2])
	if !ok {
		return usage(stderr, fmt.Sprintf("model %s: expected <provider>/<id>, got %q", slot, args[2]))
	}

	sel := childMap(b.section("models"), slot)
	sel["provider"] = provider
	sel["model"] = id

	if err := applyFlags(modelSelectFlags, args, 3, sel, "model "+slot, stderr); err != nil {
		return err
	}

	slog.Info("Model selected in shell config", "slot", slot, "provider", provider, "model", id)
	return nil
}

// modelVariantFlags is the flag surface for `model <name> variant <v>`.
// It mirrors modelSelectFlags minus the model identity, with --think
// taking an explicit value so a variant can turn off what the baseline
// turned on.
var modelVariantFlags = []flagSpec{
	{name: "--think", jsonKey: "think", kind: flagBool, op: opSet},
	{name: "--reasoning-effort", jsonKey: "reasoning_effort", kind: flagString, op: opSet},
	{name: "--max-tokens", jsonKey: "max_tokens", kind: flagInt, op: opSet, validate: nonNegative("--max-tokens")},
	{name: "--temperature", jsonKey: "temperature", kind: flagFloat, op: opSet, validate: unitRange("--temperature")},
	{name: "--top-p", jsonKey: "top_p", kind: flagFloat, op: opSet, validate: unitRange("--top-p")},
	{name: "--top-k", jsonKey: "top_k", kind: flagInt, op: opSet},
	{name: "--frequency-penalty", jsonKey: "frequency_penalty", kind: flagFloat, op: opSet, validate: finite("--frequency-penalty")},
	{name: "--presence-penalty", jsonKey: "presence_penalty", kind: flagFloat, op: opSet, validate: finite("--presence-penalty")},
	{name: "--provider-options", child: "provider_options", kind: flagJSONObject, op: opMergeChild},
}

// modelVariant declares a named parameter preset on a model config.
func modelVariant(b *ConfigBuilder, args []string, stderr io.Writer) error {
	slot := args[1]
	if len(args) < 4 {
		return usage(stderr, fmt.Sprintf("usage: model %s variant <variant> [--reasoning-effort L] [--think true|false] ...", slot))
	}
	name := args[3]
	if name == "" {
		return usage(stderr, fmt.Sprintf("model %s variant: variant name must not be empty", slot))
	}

	sel := childMap(b.section("models"), slot)
	variant := childMap(childMap(sel, "variants"), name)
	if err := applyFlags(modelVariantFlags, args, 4, variant, "model "+slot+" variant "+name, stderr); err != nil {
		return err
	}

	slog.Info("Model variant declared in shell config", "slot", slot, "variant", name)
	return nil
}
