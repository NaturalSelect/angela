package model

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/editorcontext"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestVSCodeLabel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		snap editorcontext.Snapshot
		want string
	}{
		{
			name: "disconnected",
			snap: editorcontext.Snapshot{ActiveFile: "a.go"},
			want: "",
		},
		{
			name: "connected without a file",
			snap: editorcontext.Snapshot{Connected: true},
			want: "",
		},
		{
			name: "file only",
			snap: editorcontext.Snapshot{Connected: true, ActiveFile: "a.go"},
			want: "◧ a.go",
		},
		{
			name: "one selected line",
			snap: editorcontext.Snapshot{
				Connected:  true,
				ActiveFile: "a.go",
				Selection:  &editorcontext.Selection{StartLine: 12, EndLine: 12},
			},
			want: "◧ a.go:12",
		},
		{
			name: "selected range",
			snap: editorcontext.Snapshot{
				Connected:  true,
				ActiveFile: "a.go",
				Selection:  &editorcontext.Selection{StartLine: 12, EndLine: 14},
			},
			want: "◧ a.go:12-14",
		},
		{
			name: "nested path shows the base name",
			snap: editorcontext.Snapshot{Connected: true, ActiveFile: "internal/app/app.go"},
			want: "◧ app.go",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, vscodeLabel(tt.snap))
		})
	}
}

func TestUpdate_EditorContextEvent_SetsSnapshot(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	snap := editorcontext.Snapshot{Connected: true, ActiveFile: "a.go"}

	m.Update(pubsub.Event[editorcontext.Snapshot]{Type: pubsub.UpdatedEvent, Payload: snap})

	require.Equal(t, snap, m.vscode)
}

func TestHeaderShowsTheVSCodeFile(t *testing.T) {
	pinTTLs(t)

	m := drawableUI(t, 140, 40)
	m.vscode = editorcontext.Snapshot{
		Connected:  true,
		ActiveFile: "internal/app/app.go",
		Selection:  &editorcontext.Selection{StartLine: 3, EndLine: 9},
	}

	header := ansi.Strip(strings.Join(drawFrame(m, uiChat), "\n"))
	require.Contains(t, header, "◧ app.go:3-9")

	m.vscode = editorcontext.Snapshot{}
	header = ansi.Strip(strings.Join(drawFrame(m, uiChat), "\n"))
	require.NotContains(t, header, "◧")
}
