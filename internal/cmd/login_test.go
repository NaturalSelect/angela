package cmd

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/oauth"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
)

func TestLoginCmd_Aliases(t *testing.T) {
	t.Parallel()

	require.Equal(t, "auth", loginCmd.Aliases[0])
}

func TestLoginCmd_ForceFlag(t *testing.T) {
	t.Parallel()

	flag := loginCmd.Flags().Lookup("force")
	require.NotNil(t, flag)
	require.Equal(t, "f", flag.Shorthand)
}

// TestGetLoginContext_CreatesValidContext mirrors the equivalent logout
// test: both build a context.Context via signal.NotifyContext the same way.
func TestGetLoginContext_CreatesValidContext(t *testing.T) {
	ctx := getLoginContext()
	require.NotNil(t, ctx)
	require.NoError(t, ctx.Err())
}

// TestWaitEnter_ReturnsOnNewline proves waitEnter unblocks once a newline
// arrives on stdin, rather than hanging forever.
func TestWaitEnter_ReturnsOnNewline(t *testing.T) {
	swapStdinPipe(t, "\n")
	waitEnter()
}

// fakeConfigWorkspace stubs workspace.Workspace with only Config()
// overridden. Every other method panics if called, which is fine: the
// loginCopilot branch under test never reaches them.
type fakeConfigWorkspace struct {
	workspace.Workspace
	cfg *config.Config
}

func (f *fakeConfigWorkspace) Config() *config.Config { return f.cfg }

// TestLoginCopilot_AlreadyLoggedInSkipsReauth covers the only branch of
// loginCopilot reachable without a real GitHub device-code network flow:
// an existing OAuth token short-circuits before any network call.
func TestLoginCopilot_AlreadyLoggedInSkipsReauth(t *testing.T) {
	getOutput := swapStdoutPipe(t)

	ws := &fakeConfigWorkspace{cfg: &config.Config{
		Providers: csync.NewMapFrom(map[string]config.ProviderConfig{
			"copilot": {OAuthToken: &oauth.Token{AccessToken: "existing"}},
		}),
	}}

	err := loginCopilot(ws, false)
	require.NoError(t, err)
	require.Contains(t, getOutput(), "already logged in")
}
