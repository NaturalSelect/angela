package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/NaturalSelect/angela/internal/client"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/oauth"
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

func TestLogoutContext_CreatesValidContext(t *testing.T) {
	ctx := getLogoutContext()
	require.NotNil(t, ctx)
	require.NoError(t, ctx.Err())
}

// removeLog records the config keys a fake server saw removed.
type removeLog struct {
	mu   sync.Mutex
	keys []string
}

func (l *removeLog) add(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.keys = append(l.keys, key)
}

func (l *removeLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.keys...)
}

// newLogoutTestClient stands up a fake server backing the two endpoints
// pickLoggedInProvider and logoutCopilot use, and returns a real
// *client.Client pointed at it plus a log of the keys any config-remove
// request named.
func newLogoutTestClient(t *testing.T, wsID string, cfg config.Config) (*client.Client, *removeLog) {
	t.Helper()
	log := &removeLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/workspaces/"+wsID+"/config":
			w.Header().Set("Content-Type", "application/json")
			require.NoError(t, json.NewEncoder(w).Encode(cfg))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workspaces/"+wsID+"/config/remove":
			var req struct {
				Scope config.Scope `json:"scope"`
				Key   string       `json:"key"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			log.add(req.Key)
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c, err := client.NewClient("", "tcp", u.Host)
	require.NoError(t, err)
	return c, log
}

// TestPickLoggedInProvider_NoneLoggedIn covers the zero-match case: no
// prompt, just a message and an empty, non-error result.
func TestPickLoggedInProvider_NoneLoggedIn(t *testing.T) {
	getOutput := swapStdoutPipe(t)
	c, _ := newLogoutTestClient(t, "ws1", config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
	})

	provider, err := pickLoggedInProvider(c, "ws1")
	require.NoError(t, err)
	require.Empty(t, provider)
	require.Contains(t, getOutput(), "not logged in to any platform")
}

// TestPickLoggedInProvider_SingleMatchAutoSelects covers the one-match
// case: it must return that provider directly without prompting (which
// would otherwise block reading a choice from stdin).
func TestPickLoggedInProvider_SingleMatchAutoSelects(t *testing.T) {
	c, _ := newLogoutTestClient(t, "ws1", config.Config{
		Providers: csync.NewMapFrom(map[string]config.ProviderConfig{
			"copilot": {OAuthToken: &oauth.Token{AccessToken: "tok"}},
		}),
	})

	provider, err := pickLoggedInProvider(c, "ws1")
	require.NoError(t, err)
	require.Equal(t, "copilot", provider)
}

// TestPickLoggedInProvider_GetConfigErrorPropagates covers a server that
// cannot serve the config at all.
func TestPickLoggedInProvider_GetConfigErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c, err := client.NewClient("", "tcp", u.Host)
	require.NoError(t, err)

	_, err = pickLoggedInProvider(c, "ws1")
	require.Error(t, err)
}

// TestLogoutCopilot_RemovesBothConfigFields pins that both the api_key and
// oauth fields are always removed: cmp.Or evaluates both call arguments
// unconditionally before picking the first error, so neither removal is
// ever short-circuited by the other's result.
func TestLogoutCopilot_RemovesBothConfigFields(t *testing.T) {
	getOutput := swapStdoutPipe(t)
	c, log := newLogoutTestClient(t, "ws1", config.Config{})

	err := logoutCopilot(c, "ws1")
	require.NoError(t, err)
	require.Contains(t, getOutput(), "Successfully logged out")
	require.ElementsMatch(t, []string{"providers.copilot.api_key", "providers.copilot.oauth"}, log.all())
}

// TestLogoutCopilot_PropagatesRemoveError covers a server that rejects the
// config removal.
func TestLogoutCopilot_PropagatesRemoveError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	c, err := client.NewClient("", "tcp", u.Host)
	require.NoError(t, err)

	require.Error(t, logoutCopilot(c, "ws1"))
}
