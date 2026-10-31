package model

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/completions"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// NOTE: Update ends with a shared tail (placeholder refresh, the TTL
// backstop, the attachments update). Several branches return before it, and
// others fall through to it, so a refactor that moves branch bodies out of
// Update must keep each path on its side. The caches start stale, so the
// backstop sets busyFetchInFlight exactly when the tail ran.
func TestUpdate_SharedTailRunsOnlyOnFallThroughPaths(t *testing.T) {
	t.Parallel()

	withDialog := func(m *UI) {
		m.dialog = dialog.NewOverlay(idOnlyDialog{id: dialog.QuitID})
	}
	chatClick := func(m *UI) tea.Msg {
		return tea.MouseClickMsg{X: m.layout.main.Min.X, Y: m.layout.main.Min.Y}
	}

	tests := []struct {
		name     string
		prepare  func(t *testing.T, m *UI) tea.Msg
		wantTail bool
	}{
		// Paths that return before the tail.
		{
			name: "click with a dialog open",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				withDialog(m)
				return tea.MouseClickMsg{X: 5, Y: 5}
			},
		},
		{
			name: "motion with a dialog open",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				withDialog(m)
				return tea.MouseMotionMsg{X: 5, Y: 5}
			},
		},
		{
			name: "release with a dialog open",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				withDialog(m)
				return tea.MouseReleaseMsg{X: 5, Y: 5}
			},
		},
		{
			name: "wheel with a dialog open",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				withDialog(m)
				return common.CoalescedWheelMsg{Mouse: tea.Mouse{X: -100, Y: -100}, DeltaY: 1}
			},
		},
		{
			name: "click handled by the active inline editor",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				editor := newMockInlineEditor(gomock.NewController(t))
				editor.MockMouseClickableEditor.EXPECT().HandleMouseClick(gomock.Any(), gomock.Any()).Return(true, true)
				m.activeInline = editor
				m.focus = uiFocusEditor
				return tea.MouseClickMsg{X: 5, Y: 5}
			},
		},
		{
			name: "click on the completions popup",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				m.completions.SetSkills([]completions.SkillCompletionValue{{Name: "jq"}})
				m.completionsOpen = true
				m.completionsRect = image.Rect(0, 0, 20, 1)
				return tea.MouseClickMsg{X: 0, Y: 0, Button: uv.MouseLeft}
			},
		},
		{
			name: "wheel over the scrollable inline editor",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				editor := newMockInlineEditor(gomock.NewController(t))
				editor.MockWheelScrollable.EXPECT().HandleWheel(gomock.Any(), gomock.Any())
				m.activeInline = editor
				return common.CoalescedWheelMsg{
					Mouse:  tea.Mouse{X: m.layout.editor.Min.X, Y: m.layout.editor.Min.Y},
					DeltaY: 1,
				}
			},
		},
		{
			name: "paste handled by the active inline editor",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				editor := newMockInlineEditor(gomock.NewController(t))
				editor.MockPasteableEditor.EXPECT().HandlePaste(tea.PasteMsg{Content: "pasted"}).Return(nil)
				m.activeInline = editor
				m.focus = uiFocusEditor
				return tea.PasteMsg{Content: "pasted"}
			},
		},
		{
			name: "mcp state changed",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				return pubsub.Event[mcp.Event]{Payload: mcp.Event{Type: mcp.EventStateChanged}}
			},
		},
		{
			name: "mcp prompts list changed",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				return pubsub.Event[mcp.Event]{Payload: mcp.Event{Type: mcp.EventPromptsListChanged, Name: "srv"}}
			},
		},
		{
			name: "mcp tools list changed",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				return pubsub.Event[mcp.Event]{Payload: mcp.Event{Type: mcp.EventToolsListChanged, Name: "srv"}}
			},
		},
		{
			name: "mcp resources list changed",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				return pubsub.Event[mcp.Event]{Payload: mcp.Event{Type: mcp.EventResourcesListChanged, Name: "srv"}}
			},
		},

		// Paths that fall through to the tail.
		{
			name: "click on the chat",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				return chatClick(m)
			},
			wantTail: true,
		},
		{
			name: "click outside the completions popup",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				m.completions.SetSkills([]completions.SkillCompletionValue{{Name: "jq"}})
				m.completionsOpen = true
				m.completionsRect = image.Rect(0, 0, 20, 1)
				return tea.MouseClickMsg{X: 50, Y: 50, Button: uv.MouseLeft}
			},
			wantTail: true,
		},
		{
			name: "click the active inline editor does not handle",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				editor := newMockInlineEditor(gomock.NewController(t))
				editor.MockMouseClickableEditor.EXPECT().HandleMouseClick(gomock.Any(), gomock.Any()).Return(false, false)
				m.activeInline = editor
				m.focus = uiFocusEditor
				return chatClick(m)
			},
			wantTail: true,
		},
		{
			name: "motion over the chat",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				return tea.MouseMotionMsg{X: m.layout.main.Min.X, Y: 0}
			},
			wantTail: true,
		},
		{
			name: "release over the chat",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				return tea.MouseReleaseMsg{X: m.layout.main.Min.X, Y: m.layout.main.Min.Y}
			},
			wantTail: true,
		},
		{
			name: "wheel over the chat",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				return common.CoalescedWheelMsg{Mouse: tea.Mouse{X: -100, Y: -100}, DeltaX: 1, DeltaY: 1}
			},
			wantTail: true,
		},
		{
			name: "plain paste",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				m.focus = uiFocusEditor
				return tea.PasteMsg{Content: "hello world"}
			},
			wantTail: true,
		},
		{
			name: "mcp event of a type Update does not route",
			prepare: func(t *testing.T, m *UI) tea.Msg {
				return pubsub.Event[mcp.Event]{Payload: mcp.Event{Type: mcp.EventChannelMessage}}
			},
			wantTail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, _ := newMockBusyUI(t)
			m.updateLayoutAndSize()
			msg := tt.prepare(t, m)
			require.False(t, m.busyFetchInFlight, "the probe must start unset")

			m.Update(msg)

			if tt.wantTail {
				require.True(t, m.busyFetchInFlight, "this path must fall through to the shared tail")
			} else {
				require.False(t, m.busyFetchInFlight, "this path must return before the shared tail")
			}
		})
	}
}

// NOTE: A click on an attachment's remove button returns before the tail,
// while the same click with another button falls through to it.
func TestUpdate_AttachmentRemoveClickSkipsSharedTail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		button   tea.MouseButton
		wantTail bool
	}{
		{name: "left button removes the attachment", button: uv.MouseLeft},
		{name: "right button falls through", button: uv.MouseRight, wantTail: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			u, removeX := newAttachmentClickTestUI(t)
			require.False(t, u.busyFetchInFlight, "the probe must start unset")

			u.Update(tea.MouseClickMsg(tea.Mouse{
				X:      u.layout.editor.Min.X + removeX,
				Y:      u.layout.editor.Min.Y,
				Button: tt.button,
			}))

			require.Equal(t, tt.wantTail, u.busyFetchInFlight)
		})
	}
}
