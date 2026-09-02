package workspace

import (
	"errors"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/question"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// permissionEditAccess is an access that no rung of the permission
// ladder settles on its own (mirrors internal/permission's own
// editAccess test helper), so Gate always reaches the prompt and
// leaves something pending for Grant/GrantPersistent/Deny to resolve.
func permissionEditAccess(path string) permission.Access {
	return permission.Access{Tool: "edit", Action: permission.ActionEdit, Path: path}
}

// TestAppWorkspace_PermissionResolution drives AppWorkspace's
// Permission passthroughs against the real permission.Service that
// app.NewForTest wires up (rather than a mock), because the behavior
// worth pinning here is resolution semantics owned by that service:
// the first Grant/GrantPersistent/Deny call settles a pending request
// and unblocks Gate with the matching outcome, and a second call on
// the same request has nothing left to resolve.
func TestAppWorkspace_PermissionResolution(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		resolve     func(*AppWorkspace, permission.PermissionRequest) bool
		wantAllowed bool
	}{
		{name: "Grant allows the call", resolve: (*AppWorkspace).PermissionGrant, wantAllowed: true},
		{name: "GrantPersistent allows the call", resolve: (*AppWorkspace).PermissionGrantPersistent, wantAllowed: true},
		{name: "Deny refuses the call", resolve: (*AppWorkspace).PermissionDeny, wantAllowed: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := newAWFixture(t)

			events := fx.app.Permissions.Subscribe(t.Context())
			decision := make(chan permission.Decision, 1)
			go func() {
				decision <- fx.app.Permissions.Gate(t.Context(), permission.GateRequest{
					SessionID:  "sess-1",
					ToolCallID: "call-1",
					Access:     permissionEditAccess("/tmp/x"),
				})
			}()

			var req permission.PermissionRequest
			select {
			case ev := <-events:
				req = ev.Payload
			case <-time.After(2 * time.Second):
				t.Fatal("permission request was never published")
			}

			require.True(t, tc.resolve(fx.ws, req), "first resolution should settle the pending request")
			require.False(t, tc.resolve(fx.ws, req), "second resolution has nothing left to settle")

			select {
			case got := <-decision:
				require.Equal(t, tc.wantAllowed, got.Allowed())
			case <-time.After(2 * time.Second):
				t.Fatal("Gate never returned a decision")
			}
		})
	}
}

func TestAppWorkspace_PermissionMode(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)

	require.Equal(t, permission.ModeManual, fx.ws.PermissionMode())

	fx.ws.PermissionSetMode(permission.ModeYolo)
	require.Equal(t, permission.ModeYolo, fx.ws.PermissionMode())
}

// TestAppWorkspace_QuestionAnswer drives AppWorkspace's Question
// passthroughs against the real question.Service from app.NewForTest,
// pinning that Answer both unblocks a pending Ask with the given
// answers and reports false once nothing is pending anymore.
func TestAppWorkspace_QuestionAnswer(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)

	events := fx.app.Questions.Subscribe(t.Context())
	type askResult struct {
		answers []question.Answer
		err     error
	}
	result := make(chan askResult, 1)
	go func() {
		answers, err := fx.app.Questions.Ask(t.Context(), question.Request{
			ID: "q1",
			Questions: []question.Question{
				{ID: "q1", Type: question.TypeYesNo, Text: "Continue?", Description: "d"},
			},
		})
		result <- askResult{answers, err}
	}()

	select {
	case <-events:
	case <-time.After(2 * time.Second):
		t.Fatal("question was never published")
	}

	yes := true
	want := []question.Answer{{QuestionID: "q1", Yes: &yes}}
	require.True(t, fx.ws.QuestionAnswer(want), "first answer should resolve the pending question")

	select {
	case got := <-result:
		require.NoError(t, got.err)
		require.Equal(t, want, got.answers)
	case <-time.After(2 * time.Second):
		t.Fatal("Ask never returned")
	}

	// Ask returning guarantees its deferred cleanup already ran, so
	// this second call deterministically has nothing left to resolve.
	require.False(t, fx.ws.QuestionAnswer(want), "nothing left to resolve")
}

// TestAppWorkspace_QuestionCancel pins that Cancel unblocks a pending
// Ask with question.ErrCancelled, and that a second Cancel call has
// nothing left to cancel.
func TestAppWorkspace_QuestionCancel(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)

	events := fx.app.Questions.Subscribe(t.Context())
	errCh := make(chan error, 1)
	go func() {
		_, err := fx.app.Questions.Ask(t.Context(), question.Request{
			ID: "q2",
			Questions: []question.Question{
				{ID: "q1", Type: question.TypeYesNo, Text: "Continue?", Description: "d"},
			},
		})
		errCh <- err
	}()

	select {
	case <-events:
	case <-time.After(2 * time.Second):
		t.Fatal("question was never published")
	}

	require.True(t, fx.ws.QuestionCancel(), "first cancel should resolve the pending question")

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, question.ErrCancelled)
	case <-time.After(2 * time.Second):
		t.Fatal("Ask never returned")
	}

	// Ask returning guarantees its deferred cleanup already ran, so
	// this second call deterministically has nothing left to cancel.
	require.False(t, fx.ws.QuestionCancel(), "nothing left to cancel")
}

func TestAppWorkspace_FileTrackerRecordRead(t *testing.T) {
	t.Parallel()
	fx := newAWFixture(t)
	fx.files.EXPECT().RecordRead(gomock.Any(), "sess-1", "/tmp/a.go")

	fx.ws.FileTrackerRecordRead(t.Context(), "sess-1", "/tmp/a.go")
}

func TestAppWorkspace_FileTrackerLastReadTime(t *testing.T) {
	t.Parallel()

	t.Run("returns recorded time", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		want := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		fx.files.EXPECT().LastReadTime(gomock.Any(), "sess-1", "/tmp/a.go").Return(want)

		require.Equal(t, want, fx.ws.FileTrackerLastReadTime(t.Context(), "sess-1", "/tmp/a.go"))
	})

	t.Run("zero time when never read", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		fx.files.EXPECT().LastReadTime(gomock.Any(), "sess-1", "/tmp/missing.go").Return(time.Time{})

		require.True(t, fx.ws.FileTrackerLastReadTime(t.Context(), "sess-1", "/tmp/missing.go").IsZero())
	})
}

func TestAppWorkspace_FileTrackerListReadFiles(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		want := []string{"/tmp/a.go", "/tmp/b.go"}
		fx.files.EXPECT().ListReadFiles(gomock.Any(), "sess-1").Return(want, nil)

		got, err := fx.ws.FileTrackerListReadFiles(t.Context(), "sess-1")
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("propagates service error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		boom := errors.New("query failed")
		fx.files.EXPECT().ListReadFiles(gomock.Any(), "sess-1").Return(nil, boom)

		_, err := fx.ws.FileTrackerListReadFiles(t.Context(), "sess-1")
		require.ErrorIs(t, err, boom)
	})
}

func TestAppWorkspace_ListSessionHistory(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		want := []history.File{{ID: "f1", SessionID: "sess-1", Path: "a.go"}}
		fx.history.EXPECT().ListBySession(gomock.Any(), "sess-1").Return(want, nil)

		got, err := fx.ws.ListSessionHistory(t.Context(), "sess-1")
		require.NoError(t, err)
		require.Equal(t, want, got)
	})

	t.Run("propagates service error", func(t *testing.T) {
		t.Parallel()
		fx := newAWFixture(t)
		boom := errors.New("query failed")
		fx.history.EXPECT().ListBySession(gomock.Any(), "sess-1").Return(nil, boom)

		_, err := fx.ws.ListSessionHistory(t.Context(), "sess-1")
		require.ErrorIs(t, err, boom)
	})
}
