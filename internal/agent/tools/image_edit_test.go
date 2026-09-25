package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/imagegen"
	"github.com/NaturalSelect/angela/internal/images"
)

func TestImageEditResolvesImageIDsIntoSourceImageIDs(t *testing.T) {
	t.Parallel()

	const sessionID = "sess-edit"
	store := newTestImageStore(t, sessionID)

	seed, err := store.Create(t.Context(), images.CreateParams{
		SessionID: sessionID,
		Prompt:    "a cat",
		Provider:  "openai",
		Model:     "gpt-image-1",
		MIMEType:  "image/png",
		Data:      fixturePNG(t, 512, 512),
	})
	require.NoError(t, err)

	fake := &fakeImageClient{
		model: "gpt-image-1",
		editOutput: imagegen.Output{
			Data:     fixturePNG(t, 512, 512),
			MIMEType: "image/png",
		},
	}
	tool := NewImageEditTool(imageClientFactory(fake), store, t.TempDir())

	ctx := imageSessionContext(t, sessionID)
	resp := runImageTool(t, tool, ctx, "call-1", ImageEditParams{
		Prompt:   "add a hat",
		ImageIDs: []string{seed.ID},
	})

	require.False(t, resp.IsError, "response: %+v", resp)
	require.Equal(t, "image", resp.Type)
	require.Equal(t, 1, fake.editCalls)
	require.Len(t, fake.lastEdit.Sources, 1)
	require.Equal(t, "image/png", fake.lastEdit.Sources[0].MIMEType)
	require.Equal(t, seed.Data, fake.lastEdit.Sources[0].Data)

	var meta ImageResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, []string{seed.ID}, meta.SourceImageIDs)
	require.Contains(t, resp.Content, meta.ImageID)

	stored, err := store.Get(t.Context(), meta.ImageID)
	require.NoError(t, err)
	require.Equal(t, []string{seed.ID}, stored.SourceImageIDs,
		"image_ids must resolve from the DB and end up as SourceImageIDs on the stored row")
}

func TestImageEditFilePathOutsideWorkingDirFails(t *testing.T) {
	t.Parallel()

	const sessionID = "sess-edit-outside"
	store := newTestImageStore(t, sessionID)
	workingDir := t.TempDir()
	outside := t.TempDir()

	outsideFile := filepath.Join(outside, "source.png")
	require.NoError(t, os.WriteFile(outsideFile, fixturePNG(t, 64, 64), 0o644))

	fake := &fakeImageClient{model: "gpt-image-1"}
	tool := NewImageEditTool(imageClientFactory(fake), store, workingDir)

	ctx := imageSessionContext(t, sessionID)
	resp := runImageTool(t, tool, ctx, "call-1", ImageEditParams{
		Prompt:    "add a hat",
		FilePaths: []string{outsideFile},
	})

	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "outside the working directory")
	require.Equal(t, 0, fake.editCalls, "a path outside workingDir must never reach the network")
}

func TestImageEditRejectsZeroSources(t *testing.T) {
	t.Parallel()

	store := newTestImageStore(t, "sess")
	fake := &fakeImageClient{model: "gpt-image-1"}
	tool := NewImageEditTool(imageClientFactory(fake), store, t.TempDir())

	ctx := imageSessionContext(t, "sess")
	resp := runImageTool(t, tool, ctx, "call-1", ImageEditParams{Prompt: "add a hat"})

	require.True(t, resp.IsError)
	require.Equal(t, 0, fake.editCalls, "zero sources must fail validation before any client call")
}

func TestImageEditRejectsTooManySources(t *testing.T) {
	t.Parallel()

	store := newTestImageStore(t, "sess")
	fake := &fakeImageClient{model: "gpt-image-1"}
	tool := NewImageEditTool(imageClientFactory(fake), store, t.TempDir())

	ids := make([]string, 17)
	for i := range ids {
		ids[i] = fmt.Sprintf("img_nonexistent%02d", i)
	}

	ctx := imageSessionContext(t, "sess")
	resp := runImageTool(t, tool, ctx, "call-1", ImageEditParams{Prompt: "add a hat", ImageIDs: ids})

	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "too many source images")
	require.Equal(t, 0, fake.editCalls,
		"17 sources must fail validation before any of the (nonexistent) ids are even looked up")
}

func TestImageEditUnknownImageIDNamesIt(t *testing.T) {
	t.Parallel()

	store := newTestImageStore(t, "sess")
	fake := &fakeImageClient{model: "gpt-image-1"}
	tool := NewImageEditTool(imageClientFactory(fake), store, t.TempDir())

	ctx := imageSessionContext(t, "sess")
	resp := runImageTool(t, tool, ctx, "call-1", ImageEditParams{
		Prompt:   "add a hat",
		ImageIDs: []string{"img_doesnotexist"},
	})

	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "img_doesnotexist")
	require.Equal(t, 0, fake.editCalls)
}

func TestImageEditClientErrorFailsCleanly(t *testing.T) {
	t.Parallel()

	const sessionID = "sess-edit-err"
	store := newTestImageStore(t, sessionID)
	seed, err := store.Create(t.Context(), images.CreateParams{
		SessionID: sessionID,
		Prompt:    "a dog",
		Provider:  "openai",
		Model:     "gpt-image-1",
		MIMEType:  "image/png",
		Data:      fixturePNG(t, 128, 128),
	})
	require.NoError(t, err)

	fake := &fakeImageClient{model: "gpt-image-1", editErr: errors.New("rate limited")}
	tool := NewImageEditTool(imageClientFactory(fake), store, t.TempDir())

	ctx := imageSessionContext(t, sessionID)
	resp := runImageTool(t, tool, ctx, "call-1", ImageEditParams{
		Prompt:   "add a hat",
		ImageIDs: []string{seed.ID},
	})

	require.True(t, resp.IsError, "a client error must surface as a Fail result, not a panic")
	require.Contains(t, resp.Content, "rate limited")
}

func TestImageEditRejectsUnsupportedFileContentType(t *testing.T) {
	t.Parallel()

	store := newTestImageStore(t, "sess")
	workingDir := t.TempDir()
	textFile := filepath.Join(workingDir, "notes.txt")
	require.NoError(t, os.WriteFile(textFile,
		[]byte("just some plain text, not an image at all, padded out a little further for good measure"), 0o644))

	fake := &fakeImageClient{model: "gpt-image-1"}
	tool := NewImageEditTool(imageClientFactory(fake), store, workingDir)

	ctx := imageSessionContext(t, "sess")
	resp := runImageTool(t, tool, ctx, "call-1", ImageEditParams{
		Prompt:    "add a hat",
		FilePaths: []string{"notes.txt"},
	})

	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "unsupported content type")
	require.Equal(t, 0, fake.editCalls)
}

func TestImageEditRejectsOversizedFile(t *testing.T) {
	t.Parallel()

	store := newTestImageStore(t, "sess")
	workingDir := t.TempDir()
	bigFile := filepath.Join(workingDir, "big.png")

	f, err := os.Create(bigFile)
	require.NoError(t, err)
	_, err = f.Write(fixturePNG(t, 8, 8))
	require.NoError(t, err)
	require.NoError(t, f.Truncate(maxSourceImageBytes+1))
	require.NoError(t, f.Close())

	fake := &fakeImageClient{model: "gpt-image-1"}
	tool := NewImageEditTool(imageClientFactory(fake), store, workingDir)

	ctx := imageSessionContext(t, "sess")
	resp := runImageTool(t, tool, ctx, "call-1", ImageEditParams{
		Prompt:    "add a hat",
		FilePaths: []string{"big.png"},
	})

	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "too large")
	require.Equal(t, 0, fake.editCalls)
}

func TestImageEditMixesImageIDsAndFilePaths(t *testing.T) {
	t.Parallel()

	const sessionID = "sess-edit-mixed"
	store := newTestImageStore(t, sessionID)
	workingDir := t.TempDir()

	seed, err := store.Create(t.Context(), images.CreateParams{
		SessionID: sessionID,
		Prompt:    "a cat",
		Provider:  "openai",
		Model:     "gpt-image-1",
		MIMEType:  "image/png",
		Data:      fixturePNG(t, 32, 32),
	})
	require.NoError(t, err)

	filePath := filepath.Join(workingDir, "extra.png")
	require.NoError(t, os.WriteFile(filePath, fixturePNG(t, 16, 16), 0o644))

	fake := &fakeImageClient{
		model: "gpt-image-1",
		editOutput: imagegen.Output{
			Data:     fixturePNG(t, 32, 32),
			MIMEType: "image/png",
		},
	}
	tool := NewImageEditTool(imageClientFactory(fake), store, workingDir)

	ctx := imageSessionContext(t, sessionID)
	resp := runImageTool(t, tool, ctx, "call-1", ImageEditParams{
		Prompt:    "combine them",
		ImageIDs:  []string{seed.ID},
		FilePaths: []string{"extra.png"},
	})

	require.False(t, resp.IsError, "response: %+v", resp)
	require.Len(t, fake.lastEdit.Sources, 2)
}
