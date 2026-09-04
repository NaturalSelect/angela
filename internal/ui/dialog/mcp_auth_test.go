package dialog

import (
	"errors"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	mcptools "github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// newTestMCPAuth builds the dialog with the given pending servers and
// authURLFn, mirroring the app's construction.
func newTestMCPAuth(t *testing.T, pending []mcptools.PendingAuthServer, authURLFn func(string) string) *MCPAuth {
	t.Helper()
	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	m, cmd := NewMCPAuth(com, pending, authURLFn)
	require.NotNil(t, cmd, "construction must start the spinner")
	return m
}

func onePendingServer() []mcptools.PendingAuthServer {
	return []mcptools.PendingAuthServer{{Name: "server-a", URL: "https://a.example.com"}}
}

func twoPendingServers() []mcptools.PendingAuthServer {
	return []mcptools.PendingAuthServer{
		{Name: "server-a", URL: "https://a.example.com"},
		{Name: "server-b", URL: "https://b.example.com"},
	}
}

func TestMCPAuth_ID(t *testing.T) {
	t.Parallel()

	m := newTestMCPAuth(t, onePendingServer(), nil)
	require.Equal(t, MCPAuthID, m.ID())
}

// TestMCPAuth_HandleMsg_Close verifies escape always cancels any
// in-progress authentication and closes the dialog.
func TestMCPAuth_HandleMsg_Close(t *testing.T) {
	t.Parallel()

	m := newTestMCPAuth(t, onePendingServer(), nil)
	canceled := false
	m.cancelAuth = func() { canceled = true }

	action := m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, ActionClose{}, action)
	require.True(t, canceled, "closing must cancel any in-progress auth")
	require.Nil(t, m.cancelAuth)
}

// TestMCPAuth_HandleMsg_SpinnerTick verifies the spinner only animates
// while the flow is still in progress.
func TestMCPAuth_HandleMsg_SpinnerTick(t *testing.T) {
	t.Parallel()

	t.Run("prompt state keeps animating", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		action := m.HandleMsg(spinner.TickMsg{})
		_, ok := action.(ActionCmd)
		require.True(t, ok)
	})

	t.Run("authenticating state keeps animating", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		m.state = MCPAuthStateAuthenticating
		action := m.HandleMsg(spinner.TickMsg{})
		_, ok := action.(ActionCmd)
		require.True(t, ok)
	})

	t.Run("success state ignores ticks", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		m.state = MCPAuthStateSuccess
		require.Nil(t, m.HandleMsg(spinner.TickMsg{}))
	})

	t.Run("error state ignores ticks", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		m.state = MCPAuthStateError
		require.Nil(t, m.HandleMsg(spinner.TickMsg{}))
	})
}

// TestMCPAuth_HandleMsg_SubmitFromPrompt verifies enter starts the
// flow: it moves to the authenticating state and dispatches the
// message that tells the app to begin the real OAuth call.
func TestMCPAuth_HandleMsg_SubmitFromPrompt(t *testing.T) {
	t.Parallel()

	m := newTestMCPAuth(t, onePendingServer(), nil)
	action := m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
	_, ok := action.(ActionCmd)
	require.True(t, ok)
	require.Equal(t, MCPAuthStateAuthenticating, m.state)
	require.NotNil(t, m.cancelAuth)
}

// TestMCPAuth_HandleMsg_SubmitFromSuccess verifies enter advances to
// the next pending server, or closes when that was the last one.
func TestMCPAuth_HandleMsg_SubmitFromSuccess(t *testing.T) {
	t.Parallel()

	t.Run("more servers remain", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, twoPendingServers(), nil)
		m.state = MCPAuthStateSuccess

		action := m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Nil(t, action)
		require.Equal(t, MCPAuthStatePrompt, m.state)
		require.Equal(t, 1, m.current)
	})

	t.Run("last server closes the dialog", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		m.state = MCPAuthStateSuccess

		action := m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
		require.Equal(t, ActionClose{}, action)
	})
}

// TestMCPAuth_HandleMsg_CopyURL verifies 'c' copies the authorization
// URL once one exists, otherwise falls back to the server's plain URL,
// and does nothing when neither is available. It never invokes the
// returned command, since doing so would touch the real clipboard.
func TestMCPAuth_HandleMsg_CopyURL(t *testing.T) {
	t.Parallel()

	t.Run("authenticating with an authorization URL", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), func(string) string { return "https://auth.example.com/callback" })
		m.state = MCPAuthStateAuthenticating
		action := m.HandleMsg(tea.KeyPressMsg{Code: 'c', Text: "c"})
		_, ok := action.(ActionCmd)
		require.True(t, ok)
	})

	t.Run("prompt falls back to the server URL", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		action := m.HandleMsg(tea.KeyPressMsg{Code: 'c', Text: "c"})
		_, ok := action.(ActionCmd)
		require.True(t, ok)
	})

	t.Run("neither URL is available", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, []mcptools.PendingAuthServer{{Name: "server-a"}}, nil)
		require.Nil(t, m.HandleMsg(tea.KeyPressMsg{Code: 'c', Text: "c"}))
	})
}

// TestMCPAuth_HandleMsg_Skip verifies 's' only advances the flow from
// the prompt state.
func TestMCPAuth_HandleMsg_Skip(t *testing.T) {
	t.Parallel()

	t.Run("prompt state advances", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, twoPendingServers(), nil)
		action := m.HandleMsg(tea.KeyPressMsg{Code: 's', Text: "s"})
		require.Nil(t, action)
		require.Equal(t, 1, m.current)
	})

	t.Run("authenticating state ignores skip", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, twoPendingServers(), nil)
		m.state = MCPAuthStateAuthenticating
		require.Nil(t, m.HandleMsg(tea.KeyPressMsg{Code: 's', Text: "s"}))
		require.Equal(t, 0, m.current)
	})
}

// TestMCPAuth_HandleMsg_AuthResult verifies the two terminal messages
// the app sends back once the real OAuth call resolves.
func TestMCPAuth_HandleMsg_AuthResult(t *testing.T) {
	t.Parallel()

	t.Run("complete moves to success and drops the cancel func", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		m.cancelAuth = func() {}
		require.Nil(t, m.HandleMsg(ActionMCPAuthComplete{Name: "server-a"}))
		require.Equal(t, MCPAuthStateSuccess, m.state)
		require.Nil(t, m.cancelAuth)
	})

	t.Run("errored moves to error and records it", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		m.cancelAuth = func() {}
		boom := errors.New("boom")
		require.Nil(t, m.HandleMsg(ActionMCPAuthErrored{Name: "server-a", Error: boom}))
		require.Equal(t, MCPAuthStateError, m.state)
		require.Equal(t, boom, m.err)
		require.Nil(t, m.cancelAuth)
	})
}

// TestMCPAuth_StartAuth covers both branches directly: a server left
// to authenticate, and the (defensive) case where none remain.
func TestMCPAuth_StartAuth(t *testing.T) {
	t.Parallel()

	t.Run("a server remains", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		action := m.startAuth()
		_, ok := action.(ActionCmd)
		require.True(t, ok)
		require.Equal(t, MCPAuthStateAuthenticating, m.state)
		require.NotNil(t, m.cancelAuth)
	})

	t.Run("nothing left to authenticate", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		m.current = 1
		require.Equal(t, ActionClose{}, m.startAuth())
	})
}

// TestMCPAuth_Advance covers both branches directly: moving on to the
// next server, and finishing the last one.
func TestMCPAuth_Advance(t *testing.T) {
	t.Parallel()

	t.Run("more servers remain", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, twoPendingServers(), nil)
		canceled := false
		m.cancelAuth = func() { canceled = true }
		m.err = errors.New("stale")

		require.Nil(t, m.advance())
		require.True(t, canceled)
		require.Equal(t, 1, m.current)
		require.Equal(t, MCPAuthStatePrompt, m.state)
		require.NoError(t, m.err)
	})

	t.Run("last server closes the dialog", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		require.Equal(t, ActionClose{}, m.advance())
	})
}

// TestMCPAuth_CancelAuth verifies calling cancel is safe whether or
// not a flow is in progress.
func TestMCPAuth_CancelAuth(t *testing.T) {
	t.Parallel()

	m := newTestMCPAuth(t, onePendingServer(), nil)
	require.NotPanics(t, m.CancelAuth, "no in-progress auth must be a no-op")

	called := false
	m.cancelAuth = func() { called = true }
	m.CancelAuth()
	require.True(t, called)
	require.Nil(t, m.cancelAuth)
}

// TestMCPAuth_AuthURL verifies the authorization URL is only available
// once authURLFn is wired up, and is looked up by the current server's
// name.
func TestMCPAuth_AuthURL(t *testing.T) {
	t.Parallel()

	t.Run("no authURLFn", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		require.Empty(t, m.authURL())
	})

	t.Run("delegates to authURLFn with the current server's name", func(t *testing.T) {
		t.Parallel()
		var gotName string
		m := newTestMCPAuth(t, onePendingServer(), func(name string) string {
			gotName = name
			return "https://auth.example.com/" + name
		})
		require.Equal(t, "https://auth.example.com/server-a", m.authURL())
		require.Equal(t, "server-a", gotName)
	})
}

// TestMCPAuth_CurrentServer verifies the defensive zero value once
// every pending server has been handled.
func TestMCPAuth_CurrentServer(t *testing.T) {
	t.Parallel()

	m := newTestMCPAuth(t, onePendingServer(), nil)
	require.Equal(t, "server-a", m.currentServer().Name)

	m.current = 1
	require.Equal(t, mcptools.PendingAuthServer{}, m.currentServer())
}

// TestMCPAuth_Draw covers the visible content in every state, plus
// the progress counter shown only with more than one pending server.
func TestMCPAuth_Draw(t *testing.T) {
	t.Parallel()

	draw := func(m *MCPAuth) string {
		scr := uv.NewScreenBuffer(70, 20)
		m.Draw(scr, uv.Rect(0, 0, 70, 20))
		return ansi.Strip(scr.Render())
	}

	t.Run("prompt state", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		content := draw(m)
		require.Contains(t, content, "Authenticate with server-a")
		require.Contains(t, content, "enter")
		require.Contains(t, content, "https://a.example.com")
	})

	t.Run("prompt state with more than one pending server shows progress", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, twoPendingServers(), nil)
		require.Contains(t, draw(m), "(1/2)")
	})

	t.Run("authenticating state", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), func(string) string { return "https://auth.example.com/cb" })
		m.state = MCPAuthStateAuthenticating
		content := draw(m)
		require.Contains(t, content, "Waiting for authorization...")
		require.Contains(t, content, "https://auth.example.com/cb")
	})

	t.Run("success state", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		m.state = MCPAuthStateSuccess
		require.Contains(t, draw(m), "Authentication successful!")
	})

	t.Run("error state with a message", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		m.state = MCPAuthStateError
		m.err = errors.New("token exchange failed")
		require.Contains(t, draw(m), "token exchange failed")
	})

	t.Run("error state without a message falls back to a generic one", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		m.state = MCPAuthStateError
		require.Contains(t, draw(m), "Authentication failed.")
	})
}

// TestMCPAuth_Help verifies the help hints track the current state:
// skip only appears with more than one pending server, and the
// success label names what enter does next.
func TestMCPAuth_Help(t *testing.T) {
	t.Parallel()

	keys := func(m *MCPAuth) []string {
		var out []string
		for _, b := range m.ShortHelp() {
			out = append(out, b.Help().Key)
		}
		return out
	}

	t.Run("prompt with a single server offers no skip", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		require.NotContains(t, keys(m), "s")
	})

	t.Run("prompt with more servers offers skip", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, twoPendingServers(), nil)
		require.Contains(t, keys(m), "s")
	})

	t.Run("success label depends on whether more servers remain", func(t *testing.T) {
		t.Parallel()
		last := newTestMCPAuth(t, onePendingServer(), nil)
		last.state = MCPAuthStateSuccess
		require.Contains(t, last.ShortHelp()[0].Help().Desc, "finish")

		more := newTestMCPAuth(t, twoPendingServers(), nil)
		more.state = MCPAuthStateSuccess
		require.Contains(t, more.ShortHelp()[0].Help().Desc, "next")
	})

	t.Run("error state offers only close", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		m.state = MCPAuthStateError
		require.Equal(t, []string{"esc"}, keys(m))
	})

	t.Run("authenticating offers submit, copy, and close", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		m.state = MCPAuthStateAuthenticating
		require.ElementsMatch(t, []string{"enter", "c", "esc"}, keys(m))
	})

	t.Run("full help wraps short help in a single row", func(t *testing.T) {
		t.Parallel()
		m := newTestMCPAuth(t, onePendingServer(), nil)
		require.Len(t, m.FullHelp(), 1)
	})
}
