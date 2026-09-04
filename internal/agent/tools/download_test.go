package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

func TestNewDownloadToolRequiresURL(t *testing.T) {
	t.Parallel()

	tool := NewDownloadTool(t.TempDir(), http.DefaultClient)
	input, err := json.Marshal(DownloadParams{FilePath: "out.bin"})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "1", Name: toolnames.Download, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "URL parameter is required")
}

func TestNewDownloadToolRequiresFilePath(t *testing.T) {
	t.Parallel()

	tool := NewDownloadTool(t.TempDir(), http.DefaultClient)
	input, err := json.Marshal(DownloadParams{URL: "https://example.com/file"})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "1", Name: toolnames.Download, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "file_path parameter is required")
}

func TestNewDownloadToolRejectsBadScheme(t *testing.T) {
	t.Parallel()

	tool := NewDownloadTool(t.TempDir(), http.DefaultClient)
	input, err := json.Marshal(DownloadParams{URL: "ftp://example.com/file", FilePath: "out.bin"})
	require.NoError(t, err)

	resp, err := tool.Run(t.Context(), fantasy.ToolCall{ID: "1", Name: toolnames.Download, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "must start with http")
}

func TestNewDownloadToolRequiresSessionID(t *testing.T) {
	t.Parallel()

	tool := NewDownloadTool(t.TempDir(), http.DefaultClient)
	input, err := json.Marshal(DownloadParams{URL: "https://example.com/file", FilePath: "out.bin"})
	require.NoError(t, err)

	_, err = tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.Download, Input: string(input)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "session ID is required")
}

func TestNewDownloadToolWritesFileUnderWorkingDir(t *testing.T) {
	t.Parallel()

	body := "binary-ish content"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	tool := NewDownloadTool(dir, srv.Client())
	input, err := json.Marshal(DownloadParams{URL: srv.URL, FilePath: "sub/out.bin"})
	require.NoError(t, err)

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "1", Name: toolnames.Download, Input: string(input)})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)
	require.Contains(t, resp.Content, "Successfully downloaded")
	require.Contains(t, resp.Content, "application/octet-stream")

	written, err := os.ReadFile(filepath.Join(dir, "sub", "out.bin"))
	require.NoError(t, err)
	require.Equal(t, body, string(written))
}

func TestNewDownloadToolReportsNon200StatusAndWritesNoFile(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	tool := NewDownloadTool(dir, srv.Client())
	input, err := json.Marshal(DownloadParams{URL: srv.URL, FilePath: "out.bin"})
	require.NoError(t, err)

	ctx := context.WithValue(t.Context(), SessionIDContextKey, "session-1")
	resp, err := tool.Run(ctx, fantasy.ToolCall{ID: "1", Name: toolnames.Download, Input: string(input)})
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "404")

	_, statErr := os.Stat(filepath.Join(dir, "out.bin"))
	require.True(t, os.IsNotExist(statErr), "must not create the output file when the download fails")
}
