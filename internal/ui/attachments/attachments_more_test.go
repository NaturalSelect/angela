package attachments

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/stretchr/testify/require"
)

func testKeymap() Keymap {
	return Keymap{
		DeleteMode: key.NewBinding(key.WithKeys("ctrl+r")),
		DeleteAll:  key.NewBinding(key.WithKeys("r")),
		Escape:     key.NewBinding(key.WithKeys("esc", "alt+esc")),
	}
}

func TestUpdate_AppendsAttachment(t *testing.T) {
	t.Parallel()

	m := New(newTestRenderer(), testKeymap())
	handled := m.Update(message.Attachment{FileName: "a.txt"})
	require.True(t, handled)
	require.Len(t, m.List(), 1)
	require.Equal(t, "a.txt", m.List()[0].FileName)
}

func TestUpdate_DeleteModeActivatesWhenListNonEmpty(t *testing.T) {
	t.Parallel()

	m := New(newTestRenderer(), testKeymap())
	m.Update(message.Attachment{FileName: "a.txt"})

	handled := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	require.True(t, handled)
	require.True(t, m.deleting)
}

func TestUpdate_DeleteModeNotActivatedWhenListEmpty(t *testing.T) {
	t.Parallel()

	m := New(newTestRenderer(), testKeymap())
	handled := m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	require.True(t, handled, "the key is still handled even though nothing happens")
	require.False(t, m.deleting)
}

func TestUpdate_EscapeExitsDeleteMode(t *testing.T) {
	t.Parallel()

	m := New(newTestRenderer(), testKeymap())
	m.deleting = true

	handled := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.True(t, handled)
	require.False(t, m.deleting)
}

func TestUpdate_DeleteAllClearsList(t *testing.T) {
	t.Parallel()

	m := New(newTestRenderer(), testKeymap())
	m.Update(message.Attachment{FileName: "a.txt"})
	m.Update(message.Attachment{FileName: "b.txt"})
	m.deleting = true

	handled := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	require.True(t, handled)
	require.False(t, m.deleting)
	require.Empty(t, m.List())
}

func TestUpdate_DeleteByDigitValid(t *testing.T) {
	t.Parallel()

	m := New(newTestRenderer(), testKeymap())
	m.Update(message.Attachment{FileName: "a.txt"})
	m.Update(message.Attachment{FileName: "b.txt"})
	m.deleting = true

	handled := m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	require.True(t, handled)
	require.False(t, m.deleting)
	require.Len(t, m.List(), 1)
	require.Equal(t, "a.txt", m.List()[0].FileName)
}

func TestUpdate_DeleteByDigitOutOfRangeLeavesListUnchanged(t *testing.T) {
	t.Parallel()

	m := New(newTestRenderer(), testKeymap())
	m.Update(message.Attachment{FileName: "a.txt"})
	m.deleting = true

	handled := m.Update(tea.KeyPressMsg{Code: '9', Text: "9"})
	require.True(t, handled)
	require.False(t, m.deleting, "an out-of-range digit still exits delete mode")
	require.Len(t, m.List(), 1)
}

func TestUpdate_DeleteModeNonDigitKeyIsIgnored(t *testing.T) {
	t.Parallel()

	m := New(newTestRenderer(), testKeymap())
	m.Update(message.Attachment{FileName: "a.txt"})
	m.deleting = true

	handled := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.True(t, handled)
	require.True(t, m.deleting, "a non-digit, non-escape, non-delete-all key stays in delete mode")
	require.Len(t, m.List(), 1)
}

func TestUpdate_UnhandledKeyReturnsFalse(t *testing.T) {
	t.Parallel()

	m := New(newTestRenderer(), testKeymap())
	handled := m.Update(tea.KeyPressMsg{Code: 'z', Text: "z"})
	require.False(t, handled)
}

func TestUpdate_UnhandledMsgTypeReturnsFalse(t *testing.T) {
	t.Parallel()

	m := New(newTestRenderer(), testKeymap())
	handled := m.Update(tea.WindowSizeMsg{})
	require.False(t, handled)
}

func TestListAndReset(t *testing.T) {
	t.Parallel()

	m := New(newTestRenderer(), testKeymap())
	m.Update(message.Attachment{FileName: "a.txt"})
	require.Len(t, m.List(), 1)

	m.Reset()
	require.Empty(t, m.List())
}

func TestRendererAccessor(t *testing.T) {
	t.Parallel()

	r := newTestRenderer()
	m := New(r, testKeymap())
	require.Same(t, r, m.Renderer())
}

func TestSetStyles(t *testing.T) {
	t.Parallel()

	sty := newTestRenderer()
	normal := sty.normalStyle.Bold(true)
	deleting := sty.deletingStyle.Italic(true)
	imageSty := sty.imageStyle.Underline(true)
	textSty := sty.textStyle.Strikethrough(true)
	skillSty := sty.skillStyle.Faint(true)
	removeSty := sty.removeStyle.Reverse(true)

	sty.SetStyles(normal, deleting, imageSty, textSty, skillSty, removeSty)
	require.Equal(t, normal, sty.normalStyle)
	require.Equal(t, deleting, sty.deletingStyle)
	require.Equal(t, imageSty, sty.imageStyle)
	require.Equal(t, textSty, sty.textStyle)
	require.Equal(t, skillSty, sty.skillStyle)
	require.Equal(t, removeSty, sty.removeStyle)
}

func TestIcon(t *testing.T) {
	t.Parallel()

	r := newTestRenderer()

	tests := []struct {
		name string
		att  message.Attachment
		want lipgloss.Style
	}{
		{"image", message.Attachment{MimeType: "image/png"}, r.imageStyle},
		{"markdown", message.Attachment{MimeType: "text/markdown"}, r.skillStyle},
		{"plain text falls back to text style", message.Attachment{MimeType: "text/plain"}, r.textStyle},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, r.icon(tt.att))
		})
	}
}

func TestRender_TooManyAttachmentsShowsMoreCount(t *testing.T) {
	t.Parallel()

	r := newTestRenderer()
	atts := make([]message.Attachment, 5)
	for i := range atts {
		atts[i] = message.Attachment{FileName: fmt.Sprintf("file%d.txt", i)}
	}

	// Mirror the renderer's own per-chip width calculation so the test
	// reliably picks a width that fits fewer chips than attachments,
	// regardless of the exact styling in effect.
	maxItemWidth := lipgloss.Width(r.imageStyle.String() +
		r.normalStyle.Render(strings.Repeat("x", maxFilename)) + r.removeStyle.String())
	width := maxItemWidth * 3 // fits 2 chips, so a "3 more…" chip should appear

	out := r.Render(atts, false, true, width)
	require.Contains(t, out, "more…")
}

func TestRender_TruncatesLongFilename(t *testing.T) {
	t.Parallel()

	r := newTestRenderer()
	atts := []message.Attachment{{FileName: "a-filename-much-longer-than-fifteen-chars.txt"}}

	out := r.Render(atts, false, true, 200)
	require.Contains(t, out, "…")
	require.NotContains(t, out, "a-filename-much-longer-than-fifteen-chars.txt")
}
