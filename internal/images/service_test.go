package images

import (
	"errors"
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/db"
	"github.com/stretchr/testify/require"
)

type testEnv struct {
	svc Service
	q   *db.Queries
}

func setupTest(t *testing.T) *testEnv {
	t.Helper()

	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	q := db.New(conn)
	return &testEnv{
		svc: NewService(q),
		q:   q,
	}
}

func (e *testEnv) createSession(t *testing.T, sessionID string) {
	t.Helper()
	_, err := e.q.CreateSession(t.Context(), db.CreateSessionParams{
		ID:    sessionID,
		Title: "Test Session",
	})
	require.NoError(t, err)
}

// fixtureImageData returns a sizeable, non-uniform byte slice so a
// round-trip test can catch truncation or corruption that a
// trivial/all-zero fixture would miss.
func fixtureImageData() []byte {
	data := make([]byte, 3000)
	for i := range data {
		data[i] = byte((i*31 + 7) % 251)
	}
	return data
}

func TestService_CreateAndGet_RoundTrip(t *testing.T) {
	env := setupTest(t)
	env.createSession(t, "sess-1")

	want := fixtureImageData()
	created, err := env.svc.Create(t.Context(), CreateParams{
		SessionID:      "sess-1",
		ToolCallID:     "call-1",
		Prompt:         "a cat wearing a hat",
		RevisedPrompt:  "a photorealistic cat wearing a red hat",
		SourceImageIDs: []string{"img_aaaaaaaaaaaa", "img_bbbbbbbbbbbb"},
		Provider:       "openai",
		Model:          "gpt-image-1",
		MIMEType:       "image/png",
		Width:          1024,
		Height:         768,
		Data:           want,
	})
	require.NoError(t, err)
	require.NotEmpty(t, created.ID)
	require.True(t, strings.HasPrefix(created.ID, "img_"), "expected id to start with img_, got %q", created.ID)
	require.Positive(t, created.CreatedAt)

	got, err := env.svc.Get(t.Context(), created.ID)
	require.NoError(t, err)

	require.Equal(t, created.ID, got.ID)
	require.Equal(t, "sess-1", got.SessionID)
	require.Equal(t, "call-1", got.ToolCallID)
	require.Equal(t, "a cat wearing a hat", got.Prompt)
	require.Equal(t, "a photorealistic cat wearing a red hat", got.RevisedPrompt)
	require.Equal(t, []string{"img_aaaaaaaaaaaa", "img_bbbbbbbbbbbb"}, got.SourceImageIDs)
	require.Equal(t, "openai", got.Provider)
	require.Equal(t, "gpt-image-1", got.Model)
	require.Equal(t, "image/png", got.MIMEType)
	require.Equal(t, 1024, got.Width)
	require.Equal(t, 768, got.Height)
	require.Equal(t, created.CreatedAt, got.CreatedAt)

	// The core assertion: the BLOB must come back byte-for-byte
	// identical, not merely the same length.
	require.Equal(t, want, got.Data)
}

func TestService_Create_DefaultsEmptySourceImageIDs(t *testing.T) {
	env := setupTest(t)
	env.createSession(t, "sess-1")

	created, err := env.svc.Create(t.Context(), CreateParams{
		SessionID: "sess-1",
		Prompt:    "a dog",
		Provider:  "openai",
		Model:     "gpt-image-1",
		MIMEType:  "image/png",
		Data:      []byte{1, 2, 3},
	})
	require.NoError(t, err)
	require.Empty(t, created.SourceImageIDs)

	got, err := env.svc.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.Empty(t, got.SourceImageIDs)
}

func TestService_Get_NotFound(t *testing.T) {
	env := setupTest(t)

	_, err := env.svc.Get(t.Context(), "img_doesnotexist")
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrNotFound), "expected ErrNotFound, got %v", err)
}

func TestService_DeleteSession_CascadesGeneratedImages(t *testing.T) {
	env := setupTest(t)
	env.createSession(t, "sess-1")

	created, err := env.svc.Create(t.Context(), CreateParams{
		SessionID: "sess-1",
		Prompt:    "a cat",
		Provider:  "openai",
		Model:     "gpt-image-1",
		MIMEType:  "image/png",
		Data:      []byte{9, 9, 9},
	})
	require.NoError(t, err)

	require.NoError(t, env.q.DeleteSession(t.Context(), "sess-1"))

	_, err = env.svc.Get(t.Context(), created.ID)
	require.True(t, errors.Is(err, ErrNotFound), "expected ErrNotFound after cascading delete, got %v", err)
}
