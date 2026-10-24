package cmd

import (
	"errors"
	"sync"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/oauth"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
)

func TestLogoutCmd_Aliases(t *testing.T) {
	t.Parallel()

	require.Equal(t, "signout", logoutCmd.Aliases[0])
}

func TestLogoutCmd_HasForceFlag(t *testing.T) {
	t.Parallel()

	flag := logoutCmd.Flags().Lookup("force")
	require.NotNil(t, flag)
	require.Equal(t, "f", flag.Shorthand)
	require.Equal(t, "false", flag.DefValue)
}

func TestLogoutCmd_ValidArgs(t *testing.T) {
	t.Parallel()

	validPlatforms := map[string]bool{}
	for _, p := range logoutCmd.ValidArgs {
		validPlatforms[p] = true
	}
	require.True(t, validPlatforms["copilot"])
	require.True(t, validPlatforms["github"])
	require.True(t, validPlatforms["github-copilot"])
}

// removeRecorder is a Workspace that only records RemoveConfigField calls.
// Any other method panics through the nil embedded interface, which keeps
// the test honest about what logout is allowed to touch.
type removeRecorder struct {
	workspace.Workspace

	mu   sync.Mutex
	keys []string
	err  error
}

func (r *removeRecorder) RemoveConfigField(_ config.Scope, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = append(r.keys, key)
	return r.err
}

func (r *removeRecorder) removed() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.keys...)
}

// TestPickLoggedInProvider_NoneLoggedIn covers the zero-match case: no
// prompt, just a message and an empty, non-error result.
func TestPickLoggedInProvider_NoneLoggedIn(t *testing.T) {
	getOutput := swapStdoutPipe(t)

	provider, err := pickLoggedInProvider(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
	})
	require.NoError(t, err)
	require.Empty(t, provider)
	require.Contains(t, getOutput(), "not logged in to any platform")
}

// TestPickLoggedInProvider_SingleMatchAutoSelects covers the one-match
// case: it must return that provider directly without prompting (which
// would otherwise block reading a choice from stdin).
func TestPickLoggedInProvider_SingleMatchAutoSelects(t *testing.T) {
	provider, err := pickLoggedInProvider(&config.Config{
		Providers: csync.NewMapFrom(map[string]config.ProviderConfig{
			"copilot": {OAuthToken: &oauth.Token{AccessToken: "tok"}},
		}),
	})
	require.NoError(t, err)
	require.Equal(t, "copilot", provider)
}

// TestPickLoggedInProvider_IgnoresProvidersWithoutAToken covers a configured
// provider that holds no OAuth token: it is not something to log out of.
func TestPickLoggedInProvider_IgnoresProvidersWithoutAToken(t *testing.T) {
	getOutput := swapStdoutPipe(t)

	provider, err := pickLoggedInProvider(&config.Config{
		Providers: csync.NewMapFrom(map[string]config.ProviderConfig{
			"copilot": {},
		}),
	})
	require.NoError(t, err)
	require.Empty(t, provider)
	require.Contains(t, getOutput(), "not logged in to any platform")
}

// TestLogoutCopilot_RemovesBothConfigFields pins that both the api_key and
// oauth fields are always removed: cmp.Or evaluates both call arguments
// unconditionally before picking the first error, so neither removal is
// ever short-circuited by the other's result.
func TestLogoutCopilot_RemovesBothConfigFields(t *testing.T) {
	getOutput := swapStdoutPipe(t)
	ws := &removeRecorder{}

	require.NoError(t, logoutCopilot(ws))
	require.Contains(t, getOutput(), "Successfully logged out")
	require.ElementsMatch(t, []string{"providers.copilot.api_key", "providers.copilot.oauth"}, ws.removed())
}

// TestLogoutCopilot_PropagatesRemoveError covers a config store that
// rejects the removal.
func TestLogoutCopilot_PropagatesRemoveError(t *testing.T) {
	boom := errors.New("boom")
	ws := &removeRecorder{err: boom}

	require.ErrorIs(t, logoutCopilot(ws), boom)
	require.Len(t, ws.removed(), 2, "the second removal still runs after the first fails")
}
