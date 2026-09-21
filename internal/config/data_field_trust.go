package config

import "github.com/tidwall/gjson"

// providerTrustFields are the ProviderConfig fields covered by the
// data-field trust split. extra_headers is tracked as a single unit
// covering every key in the map: headers collectively form one
// request's identity, so letting an untrusted layer poison just one
// key while the rest keep the trusted layer's shell-expansion
// privilege offers no real safety margin. command/args/env fields on
// MCPConfig/LSPConfig are deliberately excluded everywhere in this
// file: they already grant arbitrary program execution regardless of
// whether "$(...)" inside them is allowed to run, so restricting
// command substitution there buys nothing.
var providerTrustFields = []string{"api_key", "base_url", "extra_headers"}

// mcpTrustFields are the MCPConfig fields covered by the data-field
// trust split. See providerTrustFields for why extra_headers/headers
// are tracked as a single unit and why command/args/env are excluded.
var mcpTrustFields = []string{"url", "oauth_client_id", "oauth_client_secret", "headers"}

// dataFieldTrust records, for provider and MCP config entries, which
// data fields were last set by an untrusted config layer -- one whose
// contents are not user- or admin-authored, e.g. a project-level
// angela.json that lookupConfigs auto-loads even from a freshly
// cloned, untrusted repository. A field with no entry is trusted:
// either every layer that set it was trusted, or no layer ever
// overrode it at all (e.g. a known provider's built-in catwalk
// default, which comes from Go code, not a file).
type dataFieldTrust struct {
	providers map[string]map[string]bool
	mcp       map[string]map[string]bool
}

func (t *dataFieldTrust) providerUntrusted(id, field string) bool {
	return t != nil && t.providers[id][field]
}

func (t *dataFieldTrust) mcpUntrusted(name, field string) bool {
	return t != nil && t.mcp[name][field]
}

// computeDataFieldTrust walks providers.* and mcp.* in each config
// layer, lowest to highest priority -- the same order loadFromConfigPaths
// passes to jsons.Merge -- and records, for every tracked field an
// entity ever sets, whether the layer that supplies that field's
// final, post-merge value is untrusted. jsons.Merge merges objects
// key-by-key, so a later layer's value for a key always wins; ranging
// over layers low-to-high priority and overwriting on every sighting
// naturally lands on the same winner. isTrusted classifies a layer by
// its file path.
func computeDataFieldTrust(paths []string, layers [][]byte, isTrusted func(path string) bool) *dataFieldTrust {
	trust := &dataFieldTrust{
		providers: make(map[string]map[string]bool),
		mcp:       make(map[string]map[string]bool),
	}
	for i, layer := range layers {
		trusted := isTrusted(paths[i])
		markUntrustedFields(trust.providers, layer, "providers", providerTrustFields, trusted)
		markUntrustedFields(trust.mcp, layer, "mcp", mcpTrustFields, trusted)
	}
	return trust
}

// markUntrustedFields records, for every entity under layer's topKey
// object (e.g. "providers" or "mcp") that sets one of fields, whether
// this layer -- last write wins, since callers iterate layers lowest
// to highest priority -- is untrusted.
func markUntrustedFields(dst map[string]map[string]bool, layer []byte, topKey string, fields []string, trusted bool) {
	gjson.GetBytes(layer, topKey).ForEach(func(id, entity gjson.Result) bool {
		for _, field := range fields {
			if !entity.Get(field).Exists() {
				continue
			}
			m, ok := dst[id.String()]
			if !ok {
				m = make(map[string]bool)
				dst[id.String()] = m
			}
			m[field] = !trusted
		}
		return true
	})
}

// configureProvidersOptions holds optional parameters for
// Config.configureProviders. It exists so the many existing tests that
// construct a Config/ConfigStore directly and call configureProviders
// without any data-field trust information keep compiling unchanged;
// only Load and reloadFromDiskLocked -- which compute a real
// dataFieldTrust from the on-disk layers -- need to pass it.
type configureProvidersOptions struct {
	dataTrust *dataFieldTrust
}

// configureProvidersOption customizes configureProviders.
type configureProvidersOption func(*configureProvidersOptions)

// withDataTrust supplies the per-field trust classification computed
// from the config layers that were actually merged (disk layers plus,
// when present, the workspace runtime sidecar). Omitting it leaves
// every provider/MCP data field trusted, matching the pre-trust-split
// behavior.
func withDataTrust(t *dataFieldTrust) configureProvidersOption {
	return func(o *configureProvidersOptions) { o.dataTrust = t }
}

// providerFieldResolver returns the resolver to use for a specific
// provider data field without taking any ConfigStore lock. Callers
// that already hold store.writeMu (configureProviders, invoked from
// Load and reloadFromDiskLocked while the write lock is held) must use
// this instead of the public, lock-taking ConfigStore.ProviderFieldResolver.
func providerFieldResolver(trust *dataFieldTrust, shell, envOnly VariableResolver, providerID, field string) VariableResolver {
	if trust.providerUntrusted(providerID, field) {
		return envOnly
	}
	return shell
}

// discoveryFieldResolver returns the resolver for model discovery's
// HTTP request, which resolves base_url/api_key/extra_headers through
// one shared resolver. Trust is evaluated per entity rather than per
// field here: if any of the fields discovery touches was last set by
// an untrusted layer, the whole discovery request falls back to
// env-only resolution.
func discoveryFieldResolver(trust *dataFieldTrust, shell, envOnly VariableResolver, providerID string) VariableResolver {
	for _, field := range providerTrustFields {
		if trust.providerUntrusted(providerID, field) {
			return envOnly
		}
	}
	return shell
}
