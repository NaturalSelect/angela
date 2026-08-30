package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/filetracker"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

func TestReadTextFileBoundaryCases(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "sample.txt")

	var allLines []string
	for i := range 5 {
		allLines = append(allLines, fmt.Sprintf("line %d", i+1))
	}
	require.NoError(t, os.WriteFile(filePath, []byte(strings.Join(allLines, "\n")), 0o644))

	tests := []struct {
		name        string
		offset      int
		limit       int
		wantContent string
		wantHasMore bool
	}{
		{
			name:        "exactly limit lines remaining",
			offset:      0,
			limit:       5,
			wantContent: "line 1\nline 2\nline 3\nline 4\nline 5",
			wantHasMore: false,
		},
		{
			name:        "limit plus one line remaining",
			offset:      0,
			limit:       4,
			wantContent: "line 1\nline 2\nline 3\nline 4",
			wantHasMore: true,
		},
		{
			name:        "offset at last line",
			offset:      4,
			limit:       3,
			wantContent: "line 5",
			wantHasMore: false,
		},
		{
			name:        "offset beyond eof",
			offset:      10,
			limit:       3,
			wantContent: "",
			wantHasMore: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := readTextFile(filePath, tt.offset, tt.limit, 0)
			require.NoError(t, err)
			require.Equal(t, tt.wantContent, got.content)
			require.Equal(t, tt.wantHasMore, got.hasMore)
		})
	}
}

func TestReadTextFileTruncatesLongLines(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "longline.txt")

	longLine := strings.Repeat("a", MaxLineLength+10)
	require.NoError(t, os.WriteFile(filePath, []byte(longLine), 0o644))

	got, err := readTextFile(filePath, 0, 1, 0)
	require.NoError(t, err)
	require.False(t, got.hasMore)
	require.Equal(t, strings.Repeat("a", MaxLineLength)+"...", got.content)
	require.Equal(t, 1, got.truncatedLines, "the truncation must be reported, not silent")
}

func TestReadTextFileLineExceeding1MB(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "huge_line.txt")

	hugeLine := strings.Repeat("A", 2*1024*1024) // 2MB — exceeds bufio.Scanner max
	require.NoError(t, os.WriteFile(filePath, []byte(hugeLine), 0o644))

	got, err := readTextFile(filePath, 0, 1, 0)
	require.NoError(t, err)
	require.False(t, got.hasMore)
	require.Equal(t, strings.Repeat("A", MaxLineLength)+"...", got.content)
	require.Equal(t, 1, got.truncatedLines)
}

func TestViewToolAllowsSmallSectionsOfLargeFiles(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "large.txt")
	lines := []string{strings.Repeat("a", MaxViewSize+1), "target line", "after target"}
	require.NoError(t, os.WriteFile(filePath, []byte(strings.Join(lines, "\n")), 0o644))

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	resp := runViewTool(t, tool, ctx, ViewParams{
		FilePath: filePath,
		Offset:   1,
		Limit:    1,
	})

	require.False(t, resp.IsError)
	require.Contains(t, resp.Content, "     2|target line")
	require.NotContains(t, resp.Content, "File is too large")

	var meta ViewResponseMetadata
	require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
	require.Equal(t, "target line", meta.Content)
}

func TestViewToolBlocksOversizedReturnedSections(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "large-section.txt")
	lines := make([]string, DefaultReadLimit)
	for i := range lines {
		lines[i] = strings.Repeat("a", MaxLineLength)
	}
	require.NoError(t, os.WriteFile(filePath, []byte(strings.Join(lines, "\n")), 0o644))

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	resp := runViewTool(t, tool, ctx, ViewParams{
		FilePath: filePath,
	})

	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Content section is too large")
}

func TestViewToolBlocksOversizedImages(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "large.png")
	require.NoError(t, os.WriteFile(filePath, []byte(strings.Repeat("a", MaxViewSize+1)), 0o644))

	tool := newViewToolForTest(workingDir)
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "test-session")
	ctx = context.WithValue(ctx, SupportsImagesContextKey, true)
	resp := runViewTool(t, tool, ctx, ViewParams{
		FilePath: filePath,
	})

	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "Image file is too large")
}

func TestReadTextFileEnforcesMaxContentSize(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "oversized.txt")
	lines := []string{
		strings.Repeat("a", MaxLineLength),
		strings.Repeat("b", MaxLineLength),
		"target line",
	}
	require.NoError(t, os.WriteFile(filePath, []byte(strings.Join(lines, "\n")), 0o644))

	got, err := readTextFile(filePath, 0, len(lines), MaxLineLength)
	require.ErrorAs(t, err, &contentTooLargeError{})
	require.Empty(t, got.content)
	require.False(t, got.hasMore)

	got, err = readTextFile(filePath, 2, 1, MaxLineLength)
	require.NoError(t, err)
	require.Equal(t, "target line", got.content)
	require.False(t, got.hasMore)
}

func TestReadTextFileAllowsExactMaxContentSize(t *testing.T) {
	t.Parallel()

	workingDir := t.TempDir()
	filePath := filepath.Join(workingDir, "exact-size.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("abcd\nefgh"), 0o644))

	got, err := readTextFile(filePath, 0, 2, len("abcd\nefgh"))
	require.NoError(t, err)
	require.Equal(t, "abcd\nefgh", got.content)
	require.False(t, got.hasMore)
}

func TestReadTextFileReportsAnEmptyFile(t *testing.T) {
	t.Parallel()

	filePath := filepath.Join(t.TempDir(), "empty.txt")
	require.NoError(t, os.WriteFile(filePath, nil, 0o644))

	got, err := readTextFile(filePath, 0, 10, 0)
	require.NoError(t, err)
	require.Empty(t, got.content)
	require.False(t, got.offsetPastEOF, "an empty file is not an overrun offset")
	require.Contains(t, readNotices(got, 0), "empty",
		"an empty envelope alone leaves the model guessing why")
}

func TestReadTextFileReportsAnOverrunOffset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		body           string
		wantTotalLines int
	}{
		{"trailing newline", "one\ntwo\nthree\n", 3},
		{"no trailing newline", "one\ntwo\nthree", 3},
		{"single line", "only", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			filePath := filepath.Join(t.TempDir(), "f.txt")
			require.NoError(t, os.WriteFile(filePath, []byte(tt.body), 0o644))

			got, err := readTextFile(filePath, 50, 10, 0)
			require.NoError(t, err)
			require.True(t, got.offsetPastEOF)
			require.Equal(t, tt.wantTotalLines, got.totalLines,
				"the model needs the real length to pick a valid offset")

			notice := readNotices(got, 50)
			require.Contains(t, notice, "past the end")
			require.Contains(t, notice, fmt.Sprintf("%d lines", tt.wantTotalLines))
		})
	}
}

func TestReadNoticesStaysSilentOnAnOrdinaryRead(t *testing.T) {
	t.Parallel()

	require.Empty(t, readNotices(fileRead{content: "hello\nworld"}, 0))
}

func TestReadNoticesCombinesTruncationWithMoreLines(t *testing.T) {
	t.Parallel()

	notice := readNotices(fileRead{
		content:        "a\nb",
		hasMore:        true,
		truncatedLines: 2,
	}, 4)

	require.Contains(t, notice, "were truncated")
	require.Contains(t, notice, "File has more lines")
}

type mockFileTracker struct{}

func (m mockFileTracker) RecordRead(ctx context.Context, sessionID, path string) {}

func (m mockFileTracker) LastReadTime(ctx context.Context, sessionID, path string) time.Time {
	return time.Time{}
}

func (m mockFileTracker) ListReadFiles(ctx context.Context, sessionID string) ([]string, error) {
	return nil, nil
}

func newViewToolForTest(workingDir string) fantasy.AgentTool {
	return NewViewTool(nil, mockFileTracker{}, nil, workingDir)
}

func runViewTool(t *testing.T, tool fantasy.AgentTool, ctx context.Context, params ViewParams) fantasy.ToolResponse {
	t.Helper()

	input, err := json.Marshal(params)
	require.NoError(t, err)

	call := fantasy.ToolCall{
		ID:    "test-call",
		Name:  toolnames.View,
		Input: string(input),
	}

	resp, err := tool.Run(ctx, call)
	require.NoError(t, err)
	return resp
}

var _ filetracker.Service = mockFileTracker{}

func TestReadBuiltinFile(t *testing.T) {
	t.Parallel()

	t.Run("reads angela-config skill", func(t *testing.T) {
		t.Parallel()

		resp, err := readBuiltinFile(ViewParams{
			FilePath: "angela://skills/angela-config/SKILL.md",
		}, nil)
		require.NoError(t, err)
		require.NotEmpty(t, resp.Content)
		require.Contains(t, resp.Content, "Angela Configuration")
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		resp, err := readBuiltinFile(ViewParams{
			FilePath: "angela://skills/nonexistent/SKILL.md",
		}, nil)
		require.NoError(t, err)
		require.True(t, resp.IsError)
	})

	t.Run("metadata has skill info", func(t *testing.T) {
		t.Parallel()

		resp, err := readBuiltinFile(ViewParams{
			FilePath: "angela://skills/angela-config/SKILL.md",
		}, nil)
		require.NoError(t, err)

		var meta ViewResponseMetadata
		require.NoError(t, json.Unmarshal([]byte(resp.Metadata), &meta))
		require.Equal(t, ViewResourceSkill, meta.ResourceType)
		require.Equal(t, "angela-config", meta.ResourceName)
		require.NotEmpty(t, meta.ResourceDescription)
	})

	t.Run("respects offset", func(t *testing.T) {
		t.Parallel()

		resp, err := readBuiltinFile(ViewParams{
			FilePath: "angela://skills/angela-config/SKILL.md",
			Offset:   5,
		}, nil)
		require.NoError(t, err)
		require.NotContains(t, resp.Content, "     1|")
	})
}

func TestSniffImageMimeType(t *testing.T) {
	t.Parallel()

	jpegMagic := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	pngMagic := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	gifMagic := []byte("GIF89a")
	// Minimal RIFF/WEBP header.
	webpMagic := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), make([]byte, 16)...)
	random := []byte("not an image at all, just text")

	cases := []struct {
		name     string
		data     []byte
		fallback string
		want     string
	}{
		{"jpeg bytes in .png file uses sniffed", jpegMagic, "image/png", "image/jpeg"},
		{"png bytes in .jpg file uses sniffed", pngMagic, "image/jpeg", "image/png"},
		{"gif bytes uses sniffed", gifMagic, "image/png", "image/gif"},
		{"webp bytes uses sniffed", webpMagic, "image/png", "image/webp"},
		{"matching extension and content keeps sniffed", pngMagic, "image/png", "image/png"},
		{"unsniffable content falls back", random, "image/png", "image/png"},
		{"empty content falls back", nil, "image/jpeg", "image/jpeg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, sniffImageMimeType(tc.data, tc.fallback))
		})
	}
}
