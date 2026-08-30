package env

import (
	"os"
	"strings"
)

// angelaPrefix namespaces an override for its bare-named counterpart:
// ANGELA_FOO shadows FOO wherever Angela resolves environment
// variables (provider credentials, MCP and LSP config templates), so
// a value meant only for Angela does not collide with a same-named
// variable already present in the user's shell. The shadowing is
// applied only inside this abstraction; it never touches the real
// process environment, so subprocesses Angela spawns (the bash tool,
// hooks, LSP and MCP servers) never see it.
const angelaPrefix = "ANGELA_"

type Env interface {
	Get(key string) string
	Env() []string
}

type osEnv struct{}

// Get implements Env.
func (o *osEnv) Get(key string) string {
	if v, ok := os.LookupEnv(angelaPrefix + key); ok {
		return v
	}
	return os.Getenv(key)
}

func (o *osEnv) Env() []string {
	return withAngelaOverrides(os.Environ())
}

func New() Env {
	return &osEnv{}
}

type mapEnv struct {
	m map[string]string
}

// Get implements Env.
func (m *mapEnv) Get(key string) string {
	if value, ok := m.m[angelaPrefix+key]; ok {
		return value
	}
	return m.m[key]
}

// Env implements Env.
func (m *mapEnv) Env() []string {
	env := make([]string, 0, len(m.m))
	for k, v := range m.m {
		env = append(env, k+"="+v)
	}
	return withAngelaOverrides(env)
}

func NewFromMap(m map[string]string) Env {
	if m == nil {
		m = make(map[string]string)
	}
	return &mapEnv{m: m}
}

// withAngelaOverrides appends a bare-named entry for every
// ANGELA_-prefixed variable found in pairs, so shell-style expansion
// of the returned list resolves $FOO to ANGELA_FOO's value when set.
// Appending after the original entries wins ties: expand.ListEnviron
// keeps the last occurrence of a duplicate name.
func withAngelaOverrides(pairs []string) []string {
	var overrides []string
	for _, kv := range pairs {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, angelaPrefix) {
			continue
		}
		if bare := strings.TrimPrefix(name, angelaPrefix); bare != "" {
			overrides = append(overrides, bare+"="+value)
		}
	}
	if len(overrides) == 0 {
		return pairs
	}
	merged := make([]string, 0, len(pairs)+len(overrides))
	merged = append(merged, pairs...)
	merged = append(merged, overrides...)
	return merged
}
