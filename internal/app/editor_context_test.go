package app

import (
	"context"
	"io"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/editorcontext"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/reminder"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/stretchr/testify/require"
)

func TestReminderEditor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		snap   editorcontext.Snapshot
		want   reminder.EditorContext
		wantOK bool
	}{
		{"no window", editorcontext.Snapshot{ActiveFile: "a.go"}, reminder.EditorContext{}, false},
		{"a window without a file", editorcontext.Snapshot{Connected: true}, reminder.EditorContext{}, false},
		{
			"a file with a bare cursor",
			editorcontext.Snapshot{Connected: true, ActiveFile: "a.go", RecentFiles: []string{"a.go", "b.go"}},
			reminder.EditorContext{ActiveFile: "a.go", RecentFiles: []string{"a.go", "b.go"}},
			true,
		},
		{
			"a file with a selection",
			editorcontext.Snapshot{
				Connected:  true,
				ActiveFile: "a.go",
				Selection:  &editorcontext.Selection{StartLine: 3, EndLine: 5, Text: "body"},
			},
			reminder.EditorContext{
				ActiveFile: "a.go",
				Selection:  &reminder.EditorSelection{StartLine: 3, EndLine: 5, Text: "body"},
			},
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := reminderEditor(tt.snap)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestAgentEditorContext_IsOffWithoutATracker(t *testing.T) {
	t.Parallel()

	require.Nil(t, (&App{}).agentEditorContext())
}

// TestNew_FollowsTheVSCodeWindowOnlyWhenAsked pins both switches: the front
// end that wants the connection, and the user's option that vetoes it.
func TestNew_FollowsTheVSCodeWindowOnlyWhenAsked(t *testing.T) {
	disabled := false

	tests := []struct {
		name          string
		frontEndWants bool
		option        *bool
		wantTracker   bool
	}{
		{"asked and not vetoed", true, nil, true},
		{"the front end did not ask", false, nil, false},
		{"the user turned it off", true, &disabled, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubUpdateDefault(t)

			dataDir := t.TempDir()
			conn := mustConnectTestDB(t, dataDir)
			store := newUnconfiguredStore(dataDir)
			store.Config().Options.VSCodeContext = tt.option
			store.Overrides().VSCodeContext = tt.frontEndWants

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			application, err := New(ctx, conn, store, skills.NewManager(nil, nil, nil))
			require.NoError(t, err)
			defer application.Shutdown()

			require.Equal(t, tt.wantTracker, application.editorContext != nil)
		})
	}
}

// TestApp_Subscribe_SendsTheCurrentEditorFirst pins that a TUI that starts
// listening after the tracker connected still learns what the window shows.
func TestApp_Subscribe_SendsTheCurrentEditorFirst(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	app := NewForTest(ctx)
	app.editorContext = editorcontext.New(nil, "")

	received := make(chan tea.Msg, 16)
	program := tea.NewProgram(
		subscribeTestModel{received: received},
		tea.WithoutRenderer(),
		tea.WithInput(nil),
		tea.WithOutput(io.Discard),
		tea.WithoutSignals(),
	)
	runDone := make(chan error, 1)
	go func() {
		_, err := program.Run()
		runDone <- err
	}()
	go app.Subscribe(program)

	require.Eventually(t, func() bool {
		for {
			select {
			case msg := <-received:
				if _, ok := msg.(pubsub.Event[editorcontext.Snapshot]); ok {
					return true
				}
			default:
				return false
			}
		}
	}, 5*time.Second, 10*time.Millisecond)

	program.Quit()
	require.NoError(t, <-runDone)
	cancel()
	app.ShutdownForTest()
}
