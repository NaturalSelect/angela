package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/app"
	"github.com/NaturalSelect/angela/internal/backend"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// erroringCoordinator answers both active-agent entry points with a
// fixed error, which is how a test watches the status the transport
// turns it into.
type erroringCoordinator struct {
	*MockCoordinator
	err error
}

func (e *erroringCoordinator) ActiveAgent(context.Context, string) (config.ActiveAgent, agent.Model, error) {
	return config.ActiveAgent{}, agent.Model{}, e.err
}

func (e *erroringCoordinator) EditActiveAgent(context.Context, string, config.ActiveAgentEdit) (config.ActiveAgent, error) {
	return config.ActiveAgent{}, e.err
}

// buildFailingAgentWorkspace wires a controller to a workspace whose
// coordinator always fails with err.
func buildFailingAgentWorkspace(t *testing.T, err error) (*controllerV1, string) {
	t.Helper()

	b := backend.New(context.Background(), nil, nil)
	wsID := uuid.New().String()
	coord, _ := newCoordinator(t)
	a := &app.App{AgentCoordinator: &erroringCoordinator{MockCoordinator: coord, err: err}}
	a.Sessions = newSessions(t)

	backend.InsertWorkspaceForTest(b, &backend.Workspace{
		ID:   wsID,
		Path: t.TempDir(),
		App:  a,
	})
	return &controllerV1{backend: b, server: &Server{backend: b}}, wsID
}

// TestActiveAgentErrorsCarryTheRightStatus is A5 and A6. Everything the
// coordinator could refuse used to fall through to a blanket 500: a
// session that does not exist read as a server fault rather than a 404,
// and an edit naming an unknown agent or preset told the caller to
// retry something that can never be accepted.
func TestActiveAgentErrorsCarryTheRightStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{
			name: "a session that does not exist",
			err:  fmt.Errorf("read session %q: %w", "gone", session.ErrSessionNotFound),
			want: http.StatusNotFound,
		},
		{
			name: "an agent that is not configured",
			err:  fmt.Errorf("%w: %q", agent.ErrAgentNotAvailable, "nope"),
			want: http.StatusBadRequest,
		},
		{
			name: "a preset the model does not have",
			err:  fmt.Errorf("%w: %q", agent.ErrVariantNotAvailable, "turbo"),
			want: http.StatusBadRequest,
		},
		{
			name: "a model slot the agent does not run on",
			err:  fmt.Errorf("%w: main", agent.ErrModelSlotMismatch),
			want: http.StatusBadRequest,
		},
		{
			// The control: a genuine fault must stay a 500, or the
			// mapping would be telling clients to fix requests that
			// were never wrong.
			name: "a database that is broken",
			err:  errors.New("disk I/O error"),
			want: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, wsID := buildFailingAgentWorkspace(t, tt.err)

			t.Run("read", func(t *testing.T) {
				rec := httptest.NewRecorder()
				req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
				req.SetPathValue("id", wsID)
				req.SetPathValue("sid", "s1")
				c.handleGetWorkspaceAgentSessionActiveAgent(rec, req)
				require.Equal(t, tt.want, rec.Code)
				require.NotEmpty(t, decodedMessage(t, rec), "the reason must reach the caller")
			})

			t.Run("edit", func(t *testing.T) {
				rec := httptest.NewRecorder()
				req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/",
					strings.NewReader(`{"agent":"nope"}`))
				req.SetPathValue("id", wsID)
				req.SetPathValue("sid", "s1")
				c.handlePostWorkspaceAgentSessionActiveAgent(rec, req)
				require.Equal(t, tt.want, rec.Code)
				require.NotEmpty(t, decodedMessage(t, rec))
			})
		})
	}
}

// TestAMalformedEditIsStillABadRequest keeps the decode path from being
// swallowed by the new mapping: a body that is not JSON at all was
// already a 400 and must stay one.
func TestAMalformedEditIsStillABadRequest(t *testing.T) {
	t.Parallel()

	c, wsID := buildFailingAgentWorkspace(t, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/",
		strings.NewReader("not json"))
	req.SetPathValue("id", wsID)
	req.SetPathValue("sid", "s1")

	c.handlePostWorkspaceAgentSessionActiveAgent(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func decodedMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body proto.Error
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body.Message
}
