package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/db"
	"github.com/NaturalSelect/angela/internal/imagegen"
	"github.com/NaturalSelect/angela/internal/images"
)

// fakeImageClient is a hand-written imagegen.Client for tests: a
// 3-method interface is simpler to fake by hand than to pull in a
// mocking framework for.
type fakeImageClient struct {
	model string

	generateOutput imagegen.Output
	generateErr    error
	generateCalls  int
	lastGenerate   imagegen.Request

	editOutput imagegen.Output
	editErr    error
	editCalls  int
	lastEdit   imagegen.Request
}

func (f *fakeImageClient) Model() string { return f.model }

func (f *fakeImageClient) Generate(_ context.Context, r imagegen.Request) (imagegen.Output, error) {
	f.generateCalls++
	f.lastGenerate = r
	if f.generateErr != nil {
		return imagegen.Output{}, f.generateErr
	}
	return f.generateOutput, nil
}

func (f *fakeImageClient) Edit(_ context.Context, r imagegen.Request) (imagegen.Output, error) {
	f.editCalls++
	f.lastEdit = r
	if f.editErr != nil {
		return imagegen.Output{}, f.editErr
	}
	return f.editOutput, nil
}

// imageClientFactory adapts an already-built fake client into an
// ImageClientFactory, mirroring how a real factory resolves credentials
// once per call.
func imageClientFactory(c imagegen.Client) ImageClientFactory {
	return func(context.Context) (imagegen.Client, error) { return c, nil }
}

// failingImageClientFactory simulates credential resolution failing
// before any network call is made.
func failingImageClientFactory(err error) ImageClientFactory {
	return func(context.Context) (imagegen.Client, error) { return nil, err }
}

// fixturePNG returns a valid, decodable, solid-gradient PNG of the given
// size, standing in for what a real provider would return.
func fixturePNG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x), G: uint8(y), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// newTestImageStore returns a real, SQLite-backed images.Service (the
// persistence track's own construction pattern) with sessionID already
// registered, since a generated image row carries a session foreign key.
func newTestImageStore(t *testing.T, sessionID string) images.Service {
	t.Helper()
	conn, err := db.Connect(t.Context(), t.TempDir())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	q := db.New(conn)
	_, err = q.CreateSession(t.Context(), db.CreateSessionParams{ID: sessionID, Title: "Test Session"})
	require.NoError(t, err)

	return images.NewService(q)
}

// imageSessionContext returns a context carrying sessionID the way the
// coordinator does for session-scoped tools.
func imageSessionContext(t *testing.T, sessionID string) context.Context {
	t.Helper()
	return context.WithValue(t.Context(), SessionIDContextKey, sessionID)
}

// runImageTool marshals params, runs tool as if the model had called it,
// and returns the raw fantasy.ToolResponse for inspection.
func runImageTool(t *testing.T, tool fantasy.AgentTool, ctx context.Context, callID string, params any) fantasy.ToolResponse {
	t.Helper()
	input, err := json.Marshal(params)
	require.NoError(t, err)
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: callID, Name: tool.Info().Name, Input: string(input)})
	require.NoError(t, err, "a tool function must never hand back a bare Go error")
	return resp
}

func TestImageGenerateSuccess(t *testing.T) {
	t.Parallel()

	const sessionID = "sess-generate"
	store := newTestImageStore(t, sessionID)

	fake := &fakeImageClient{
		model: "gpt-image-1",
		generateOutput: imagegen.Output{
			Data:          fixturePNG(t, 1024, 900),
			MIMEType:      "image/png",
			RevisedPrompt: "a fluffy cat wearing a small red hat",
		},
	}
	tool := NewImageGenerateTool(imageClientFactory(fake), store)

	ctx := imageSessionContext(t, sessionID)
	resp := runImageTool(t, tool, ctx, "call-1", ImageGenerateParams{Prompt: "a cat wearing a hat"})

	require.False(t, resp.IsError, "response: %+v", resp)
	require.Equal(t, "image", resp.Type, "a successful generate must be an image-type result")
	require.Equal(t, 1, fake.generateCalls)
	require.Equal(t, "a cat wearing a hat", fake.lastGenerate.Prompt)

	var meta ImageResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.NotEmpty(t, meta.ImageID)
	require.Contains(t, resp.Content, meta.ImageID,
		"the caption must name the id the model needs in order to refer back to this image")
	require.Equal(t, "image/png", meta.MIMEType)
	require.Equal(t, "gpt-image-1", meta.Model)
	require.Equal(t, "a cat wearing a hat", meta.Prompt)
	require.Equal(t, "a fluffy cat wearing a small red hat", meta.RevisedPrompt)
	require.Equal(t, 1024, meta.Width)
	require.Equal(t, 900, meta.Height)
	require.Empty(t, meta.SourceImageIDs)

	stored, err := store.Get(t.Context(), meta.ImageID)
	require.NoError(t, err, "the generated image must be persisted")
	require.Equal(t, sessionID, stored.SessionID)
	require.Equal(t, "call-1", stored.ToolCallID)
	require.Equal(t, 1024, stored.Width)
	require.Equal(t, 900, stored.Height)
	require.Equal(t, fake.generateOutput.Data, stored.Data, "the full-size original, not the preview, must be stored")

	previewImg, _, err := image.Decode(bytes.NewReader(resp.Data))
	require.NoError(t, err)
	bounds := previewImg.Bounds()
	require.LessOrEqual(t, bounds.Dx(), previewMaxDimension)
	require.LessOrEqual(t, bounds.Dy(), previewMaxDimension)
	require.True(t, bounds.Dx() == previewMaxDimension || bounds.Dy() == previewMaxDimension,
		"a 1024x900 source must actually be downscaled to hit the cap, not merely pass under it by coincidence")
}

func TestImageGenerateTransparentBackgroundPreviewIsPNG(t *testing.T) {
	t.Parallel()

	const sessionID = "sess-generate-transparent"
	store := newTestImageStore(t, sessionID)

	fake := &fakeImageClient{
		model: "gpt-image-1",
		generateOutput: imagegen.Output{
			Data:     fixturePNG(t, 100, 100),
			MIMEType: "image/png",
		},
	}
	tool := NewImageGenerateTool(imageClientFactory(fake), store)

	ctx := imageSessionContext(t, sessionID)
	resp := runImageTool(t, tool, ctx, "call-1", ImageGenerateParams{
		Prompt:     "a sticker of a star",
		Background: "transparent",
	})

	require.False(t, resp.IsError, "response: %+v", resp)
	require.Equal(t, "image/png", resp.MediaType,
		"a transparent background must keep the preview as PNG rather than lossy JPEG")
}

func TestImageGenerateRequiresPrompt(t *testing.T) {
	t.Parallel()

	store := newTestImageStore(t, "sess")
	fake := &fakeImageClient{model: "gpt-image-1"}
	tool := NewImageGenerateTool(imageClientFactory(fake), store)

	ctx := imageSessionContext(t, "sess")
	resp := runImageTool(t, tool, ctx, "call-1", ImageGenerateParams{})

	require.True(t, resp.IsError)
	require.Equal(t, 0, fake.generateCalls, "a missing prompt must fail before any network call")
}

func TestImageGenerateClientErrorFailsCleanly(t *testing.T) {
	t.Parallel()

	store := newTestImageStore(t, "sess")
	fake := &fakeImageClient{model: "gpt-image-1", generateErr: errors.New("provider exploded")}
	tool := NewImageGenerateTool(imageClientFactory(fake), store)

	ctx := imageSessionContext(t, "sess")
	resp := runImageTool(t, tool, ctx, "call-1", ImageGenerateParams{Prompt: "a cat"})

	require.True(t, resp.IsError, "a client error must surface as a Fail result, not a panic")
	require.Contains(t, resp.Content, "provider exploded")
}

func TestImageGenerateClientFactoryErrorFailsCleanly(t *testing.T) {
	t.Parallel()

	store := newTestImageStore(t, "sess")
	tool := NewImageGenerateTool(failingImageClientFactory(errors.New("no api key configured")), store)

	ctx := imageSessionContext(t, "sess")
	resp := runImageTool(t, tool, ctx, "call-1", ImageGenerateParams{Prompt: "a cat"})

	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "no api key configured")
}

func TestImageGenerateRequiresSession(t *testing.T) {
	t.Parallel()

	store := newTestImageStore(t, "sess")
	fake := &fakeImageClient{model: "gpt-image-1"}
	tool := NewImageGenerateTool(imageClientFactory(fake), store)

	resp := runImageTool(t, tool, t.Context(), "call-1", ImageGenerateParams{Prompt: "a cat"})

	require.True(t, resp.IsError)
	require.Equal(t, 0, fake.generateCalls)
}
