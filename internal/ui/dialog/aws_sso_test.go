package dialog

import (
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func newTestAWSSSO(t *testing.T) *AWSSSO {
	t.Helper()
	s := styles.CharmtonePantera()
	com := &common.Common{Styles: &s}
	m, cmd := NewAWSSSO(com, "aws sso login")
	require.NotNil(t, cmd, "construction must start the spinner")
	return m
}

// TestAWSSSO_ID verifies the dialog identifies itself for the overlay
// stack.
func TestAWSSSO_ID(t *testing.T) {
	t.Parallel()

	m := newTestAWSSSO(t)
	require.Equal(t, AWSSSOID, m.ID())
}

// TestAWSSSO_SetURLOnlyAppliesWhileWaiting verifies the verification URL
// is captured only before the flow resolves; a late-arriving update
// after Finish must not overwrite the terminal state's display.
func TestAWSSSO_SetURLOnlyAppliesWhileWaiting(t *testing.T) {
	t.Parallel()

	m := newTestAWSSSO(t)
	m.SetURL("https://example.com/verify")
	require.Equal(t, "https://example.com/verify", m.url)

	m.Finish("")
	m.SetURL("https://example.com/other")
	require.Equal(t, "https://example.com/verify", m.url, "a resolved dialog must ignore further URL updates")
}

// TestAWSSSO_Finish covers both terminal states: success clears any
// prior error, and a non-empty message moves the dialog to the error
// state instead.
func TestAWSSSO_Finish(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		m := newTestAWSSSO(t)
		m.Finish("")
		require.Equal(t, awsSSOStateSuccess, m.state)
	})

	t.Run("failure", func(t *testing.T) {
		t.Parallel()
		m := newTestAWSSSO(t)
		m.Finish("token refresh failed")
		require.Equal(t, awsSSOStateError, m.state)
		require.Equal(t, "token refresh failed", m.errMsg)
	})
}

// TestAWSSSO_HandleMsg_Spinner verifies ticks only advance the spinner
// while the flow is still waiting; a resolved dialog ignores them.
func TestAWSSSO_HandleMsg_Spinner(t *testing.T) {
	t.Parallel()

	m := newTestAWSSSO(t)
	action := m.HandleMsg(spinner.TickMsg{})
	_, ok := action.(ActionCmd)
	require.True(t, ok, "a waiting dialog must keep animating")

	m.Finish("")
	require.Nil(t, m.HandleMsg(spinner.TickMsg{}), "a resolved dialog must stop animating")
}

// TestAWSSSO_HandleMsg_Open verifies the enter key's meaning depends on
// state: it opens the browser while waiting for a URL, does nothing
// before one has arrived, and closes the dialog once the flow succeeds.
func TestAWSSSO_HandleMsg_Open(t *testing.T) {
	t.Parallel()

	t.Run("no url yet", func(t *testing.T) {
		t.Parallel()
		m := newTestAWSSSO(t)
		require.Nil(t, m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter}))
	})

	t.Run("url present", func(t *testing.T) {
		t.Parallel()
		m := newTestAWSSSO(t)
		m.SetURL("https://example.com/verify")
		action := m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
		_, ok := action.(ActionCmd)
		require.True(t, ok, "enter must open the browser once a url is known")
	})

	t.Run("already succeeded", func(t *testing.T) {
		t.Parallel()
		m := newTestAWSSSO(t)
		m.Finish("")
		require.Equal(t, ActionClose{}, m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter}))
	})
}

// TestAWSSSO_HandleMsg_Close verifies escape always closes the dialog,
// regardless of state.
func TestAWSSSO_HandleMsg_Close(t *testing.T) {
	t.Parallel()

	m := newTestAWSSSO(t)
	require.Equal(t, ActionClose{}, m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

// TestAWSSSO_Draw covers the visible content in every state: waiting
// with no url, waiting with a url, success, and error (with and
// without a detail message).
func TestAWSSSO_Draw(t *testing.T) {
	t.Parallel()

	draw := func(m *AWSSSO) string {
		scr := uv.NewScreenBuffer(60, 20)
		m.Draw(scr, uv.Rect(0, 0, 60, 20))
		return ansi.Strip(scr.Render())
	}

	t.Run("waiting with no url", func(t *testing.T) {
		t.Parallel()
		m := newTestAWSSSO(t)
		content := draw(m)
		require.Contains(t, content, "Starting aws sso login...")
	})

	t.Run("waiting with a url", func(t *testing.T) {
		t.Parallel()
		m := newTestAWSSSO(t)
		m.SetURL("https://example.com/verify")
		content := draw(m)
		require.Contains(t, content, "https://example.com/verify")
		require.Contains(t, content, "Waiting for authentication...")
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		m := newTestAWSSSO(t)
		m.Finish("")
		require.Contains(t, draw(m), "Authentication successful!")
	})

	t.Run("error with detail", func(t *testing.T) {
		t.Parallel()
		m := newTestAWSSSO(t)
		m.Finish("boom:   collapse   whitespace")
		content := draw(m)
		require.Contains(t, content, "Authentication failed.")
		require.Contains(t, content, "boom: collapse whitespace")
	})
}

// TestAWSSSO_ShortHelp verifies the help hints change with state:
// error offers only close, success offers a labeled finish, waiting
// with no url offers only close, and waiting with a url adds open.
func TestAWSSSO_ShortHelp(t *testing.T) {
	t.Parallel()

	keys := func(m *AWSSSO) []string {
		var out []string
		for _, b := range m.ShortHelp() {
			out = append(out, b.Help().Key)
		}
		return out
	}

	waiting := newTestAWSSSO(t)
	require.Equal(t, []string{"esc"}, keys(waiting))

	withURL := newTestAWSSSO(t)
	withURL.SetURL("https://example.com")
	require.ElementsMatch(t, []string{"enter", "esc"}, keys(withURL))

	success := newTestAWSSSO(t)
	success.Finish("")
	require.Contains(t, keys(success), "enter")

	failed := newTestAWSSSO(t)
	failed.Finish("boom")
	require.Equal(t, []string{"esc"}, keys(failed))

	require.Len(t, waiting.FullHelp(), 1, "full help must wrap the short help in a single row")
}
