Field reference. The procedure lives in `angela://skills/angela-config/SKILL.md`.

# slots

`slots` maps a **slot name** to the model that fills it. Two slots ship with
Angela:

- **`main`** — the workhorse, used by `coder` and most agents.
- **`chore`** — the cheap model for auxiliary work such as titles and
  summaries.

Any other slot name may be defined; it takes effect only when an agent's
`slot` field names it. A slot is mostly a pure reference — thinking mode,
sampling parameters, and the variant presets themselves all live on the
model's catalog entry under `providers.<id>.models[]` (see
`reference/providers.md`), not on the slot. The one exception is `variant`,
which only *names* one of those presets as this slot's own default.

| Field      | Type   | Notes                                                                |
| ---------- | ------ | ----------------------------------------------------------------------- |
| `provider` | string | **Required**; a key in `providers`                                      |
| `model`    | string | **Required**; the provider's model ID                                   |
| `variant`  | string | Default variant for this slot; an agent's own `variant` always wins     |

A slot that no agent's `slot` field ever names still loads fine, but logs a
startup warning since it has no effect.

```json
{
  "slots": {
    "main": { "provider": "anthropic", "model": "claude-sonnet-4-20250514" },
    "chore": { "provider": "anthropic", "model": "claude-haiku-4-20250514", "variant": "fast" }
  }
}
```
