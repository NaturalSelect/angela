Field reference. The procedure lives in `angela://skills/angela-config/SKILL.md`.

# providers

`providers` maps a provider ID to its configuration. The ID is what a
slot's `provider` field references.

| Field                  | Type              | Notes                                                          |
| ---------------------- | ----------------- | -------------------------------------------------------------- |
| `id`                   | string            | Provider identifier                                             |
| `name`                 | string            | Display name                                                    |
| `type`                 | string            | API format: `openai`, `openai-compat`, `openrouter`, `vercel`, `anthropic`, `google`, `azure`, `bedrock`, `google-vertex`, `litellm`, `llamacpp`, `lmstudio`, `ollama`, `omlx`. Defaults to `openai` |
| `use_responses`        | bool              | Force the OpenAI Responses API on or off for this provider; unset picks per model from its ID |
| `base_url`             | string            | API base URL                                                    |
| `api_key`              | string            | Shell-expanded                                                  |
| `disable`              | bool              | Default `false`                                                 |
| `flat_rate`            | bool              | Skip cost accumulation for subscription/flat-rate billing       |
| `discover_models`      | bool              | Default `true`. Fetches `/v1/models`; when `models` is also set the discovered ones are merged in and yours win. Set `false` to use only what you list |
| `system_prompt_prefix` | string            | Prefix prepended to system prompts for this provider            |
| `extra_headers`        | object            | Extra HTTP headers; values shell-expanded, empty ones dropped   |
| `extra_body`           | object            | Merged verbatim into OpenAI-compatible request bodies; **not** shell-expanded |
| `provider_options`     | object            | Provider-specific options                                       |
| `aws_auth_refresh`     | string            | Shell command run when Bedrock credentials expire               |
| `oauth`                | object            | OAuth2 token Angela persists after an interactive login (e.g. Copilot); not meant to be hand-written |
| `models`               | array             | Model catalog, see below                                        |

```json
{
  "providers": {
    "deepseek": {
      "type": "openai-compat",
      "base_url": "https://api.deepseek.com/v1",
      "api_key": "${DEEPSEEK_API_KEY:?set DEEPSEEK_API_KEY}",
      "models": [
        {
          "id": "deepseek-chat",
          "name": "DeepSeek Chat",
          "context_window": 128000,
          "default_max_tokens": 8192,
          "cost_per_1m_in": 0.27,
          "cost_per_1m_out": 1.1,
          "cost_per_1m_in_cached": 0.27,
          "cost_per_1m_out_cached": 0.07,
          "can_reason": false,
          "supports_attachments": false
        }
      ]
    }
  }
}
```

## `base_url` conventions differ by `type`

- **`anthropic`** wants the bare host (`https://api.anthropic.com`, no `/v1`)
  because the SDK appends `v1/messages` itself. A stray `/v1` or
  `/v1/messages` suffix is stripped automatically.
- **`openai`, `openai-compat`, `openrouter`** never add a version segment, so
  `base_url` must be exactly what the vendor's docs show, `/v1` included. It
  is never guessed or appended. An accidentally copied `/chat/completions` or
  `/responses` suffix is stripped.

## Entries in `models`

| Field                                             | Type   | Notes                                       |
| ------------------------------------------------- | ------ | ------------------------------------------- |
| `id`, `name`                                      | string | Required; `id` is the provider's model ID   |
| `context_window`, `default_max_tokens`            | int    | Required                                    |
| `cost_per_1m_in`, `cost_per_1m_out`               | number | Required; USD per 1M tokens                 |
| `cost_per_1m_in_cached`, `cost_per_1m_out_cached` | number | Required                                    |
| `can_reason`                                      | bool   | Required                                    |
| `supports_attachments`                            | bool   | Required                                    |
| `reasoning_levels`                                | array  | Effort levels the model accepts. A `reasoning_effort` outside this list is not sent |
| `default_reasoning_effort`                        | string | Default effort for this model               |
| `options`                                         | object | `temperature`, `top_p`, `top_k`, `frequency_penalty`, `presence_penalty`, `provider_options` |
| `think`                                           | bool   | Default thinking mode for Anthropic reasoners |
| `use_responses`                                   | bool   | Force the OpenAI Responses API on or off for this model only; overrides the provider-level setting |
| `variants`                                        | object | Named parameter presets, see below           |

## variants

A variant is a named preset layered over a model's own parameters. It carries
no provider or model ID — it is a different way to call the *same* model, so N
models with M presets stays N+M configs instead of N×M. Every field is
optional and overrides only the keys it names; `provider_options` merges key by
key. An agent selects one via its `variant` field; an unknown name silently
degrades to the baseline. A model's own `reasoning_levels` are seeded as
variants automatically, named after each level; a user-defined variant that
reuses one of those names replaces its behavior instead of adding a duplicate.

Variant fields: `think`, `reasoning_effort`, `max_tokens`, `temperature`,
`top_p`, `top_k`, `frequency_penalty`, `presence_penalty`, `provider_options`.
Values are validated at load time — `temperature`/`top_p` must be finite and
in `0`–`1`, `max_tokens` must be `0`–`200000`, and the penalties must be
finite — and a variant that fails is dropped with a warning rather than
failing the whole config load.

```json
{
  "providers": {
    "anthropic": {
      "models": [
        {
          "id": "claude-sonnet-4-20250514",
          "name": "Claude Sonnet 4",
          "context_window": 200000,
          "default_max_tokens": 16384,
          "cost_per_1m_in": 3,
          "cost_per_1m_out": 15,
          "cost_per_1m_in_cached": 0.3,
          "cost_per_1m_out_cached": 0.3,
          "can_reason": true,
          "supports_attachments": true,
          "variants": {
            "deep": { "think": true, "max_tokens": 32768 },
            "fast": { "think": false, "temperature": 0 }
          }
        }
      ]
    }
  }
}
```

A slot or an agent can name one of these variants — see `reference/slots.md`
and `reference/agents.md`.
