package util

import (
	"os"
	"path/filepath"
	"testing"

	powernap "github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

func TestPositionToByteOffset(t *testing.T) {
	tests := []struct {
		name      string
		lineText  string
		utf16Char uint32
		expected  int
	}{
		{
			name:      "ASCII only",
			lineText:  "hello world",
			utf16Char: 6,
			expected:  6,
		},
		{
			name:      "CJK characters (3 bytes each in UTF-8, 1 UTF-16 unit)",
			lineText:  "你好world",
			utf16Char: 2,
			expected:  6,
		},
		{
			name:      "CJK - position after CJK",
			lineText:  "var x = \"你好world\"",
			utf16Char: 11,
			expected:  15,
		},
		{
			name:      "Emoji (4 bytes in UTF-8, 2 UTF-16 units)",
			lineText:  "👋hello",
			utf16Char: 2,
			expected:  4,
		},
		{
			name:      "Multiple emoji",
			lineText:  "👋👋world",
			utf16Char: 4,
			expected:  8,
		},
		{
			name:      "Mixed content",
			lineText:  "Hello👋你好",
			utf16Char: 8,
			expected:  12,
		},
		{
			name:      "Position 0",
			lineText:  "hello",
			utf16Char: 0,
			expected:  0,
		},
		{
			name:      "Position beyond end",
			lineText:  "hi",
			utf16Char: 100,
			expected:  2,
		},
		{
			name:      "Empty string",
			lineText:  "",
			utf16Char: 0,
			expected:  0,
		},
		{
			name:      "Surrogate pair at start",
			lineText:  "𐐷hello",
			utf16Char: 2,
			expected:  4,
		},
		{
			name:      "ZWJ family emoji (1 grapheme, 7 runes, 11 UTF-16 units)",
			lineText:  "hello👨\u200d👩\u200d👧\u200d👦world",
			utf16Char: 16,
			expected:  30,
		},
		{
			name:      "ZWJ family emoji - offset into middle of grapheme cluster",
			lineText:  "hello👨\u200d👩\u200d👧\u200d👦world",
			utf16Char: 8,
			expected:  12,
		},
		{
			name:      "Flag emoji (1 grapheme, 2 runes, 4 UTF-16 units)",
			lineText:  "hello🇺🇸world",
			utf16Char: 9,
			expected:  13,
		},
		{
			name:      "Combining character (1 grapheme, 2 runes, 2 UTF-16 units)",
			lineText:  "caf\u0065\u0301!",
			utf16Char: 5,
			expected:  6,
		},
		{
			name:      "Skin tone modifier (1 grapheme, 2 runes, 4 UTF-16 units)",
			lineText:  "hi👋🏽bye",
			utf16Char: 6,
			expected:  10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := powernap.PositionToByteOffset(tt.lineText, tt.utf16Char)
			if result != tt.expected {
				t.Errorf("PositionToByteOffset(%q, %d) = %d, want %d",
					tt.lineText, tt.utf16Char, result, tt.expected)
			}
		})
	}
}

func TestApplyTextEdit_UTF16(t *testing.T) {
	// Test that UTF-16 offsets are correctly converted to byte offsets
	tests := []struct {
		name     string
		lines    []string
		edit     protocol.TextEdit
		expected []string
	}{
		{
			name:  "ASCII only - no conversion needed",
			lines: []string{"hello world"},
			edit: protocol.TextEdit{
				Range: protocol.Range{
					Start: protocol.Position{Line: 0, Character: 6},
					End:   protocol.Position{Line: 0, Character: 11},
				},
				NewText: "universe",
			},
			expected: []string{"hello universe"},
		},
		{
			name:  "CJK characters - edit after Chinese characters",
			lines: []string{`var x = "你好world"`},
			edit: protocol.TextEdit{
				Range: protocol.Range{
					// "你好" = 2 UTF-16 units, but 6 bytes in UTF-8
					// Position 11 is where "world" starts in UTF-16
					Start: protocol.Position{Line: 0, Character: 11},
					End:   protocol.Position{Line: 0, Character: 16},
				},
				NewText: "universe",
			},
			expected: []string{`var x = "你好universe"`},
		},
		{
			name:  "Emoji - edit after emoji (2 UTF-16 units)",
			lines: []string{`fmt.Println("👋hello")`},
			edit: protocol.TextEdit{
				Range: protocol.Range{
					// 👋 = 2 UTF-16 units, 4 bytes in UTF-8
					// Position 15 is where "hello" starts in UTF-16
					Start: protocol.Position{Line: 0, Character: 15},
					End:   protocol.Position{Line: 0, Character: 20},
				},
				NewText: "world",
			},
			expected: []string{`fmt.Println("👋world")`},
		},
		{
			name: "ZWJ family emoji - edit after grapheme cluster",
			// "hello👨‍👩‍👧‍👦world" — family is 1 grapheme but 11 UTF-16 units
			lines: []string{"hello\U0001F468\u200d\U0001F469\u200d\U0001F467\u200d\U0001F466world"},
			edit: protocol.TextEdit{
				Range: protocol.Range{
					// "hello" = 5 UTF-16 units, family = 11 UTF-16 units
					// "world" starts at UTF-16 offset 16
					Start: protocol.Position{Line: 0, Character: 16},
					End:   protocol.Position{Line: 0, Character: 21},
				},
				NewText: "earth",
			},
			expected: []string{"hello\U0001F468\u200d\U0001F469\u200d\U0001F467\u200d\U0001F466earth"},
		},
		{
			name: "ZWJ family emoji - edit splits grapheme cluster in half",
			// LSP servers can position into the middle of a grapheme cluster.
			// After "hello" (5 UTF-16 units), the ZWJ family emoji starts.
			// UTF-16 offset 7 lands between 👨 (2 units) and ZWJ, inside
			// the grapheme cluster. The byte offset for position 7 is 9
			// (5 bytes for "hello" + 4 bytes for 👨).
			lines: []string{"hello\U0001F468\u200d\U0001F469\u200d\U0001F467\u200d\U0001F466world"},
			edit: protocol.TextEdit{
				Range: protocol.Range{
					Start: protocol.Position{Line: 0, Character: 7},
					End:   protocol.Position{Line: 0, Character: 16},
				},
				NewText: "",
			},
			// Keeps "hello" + 👨 (first rune of cluster) then removes
			// the rest of the cluster, leaving "hello👨world".
			expected: []string{"hello\U0001F468world"},
		},
		{
			name: "Flag emoji - edit after flag",
			// 🇺🇸 = 2 regional indicator runes, 4 UTF-16 units, 8 bytes
			lines: []string{"hello🇺🇸world"},
			edit: protocol.TextEdit{
				Range: protocol.Range{
					Start: protocol.Position{Line: 0, Character: 9},
					End:   protocol.Position{Line: 0, Character: 14},
				},
				NewText: "earth",
			},
			expected: []string{"hello🇺🇸earth"},
		},
		{
			name: "Combining accent - edit after composed character",
			// "café!" where é = e + U+0301 (2 code points, 2 UTF-16 units)
			lines: []string{"caf\u0065\u0301!"},
			edit: protocol.TextEdit{
				Range: protocol.Range{
					// "caf" = 3, "e" = 1, U+0301 = 1, total = 5 UTF-16 units
					Start: protocol.Position{Line: 0, Character: 5},
					End:   protocol.Position{Line: 0, Character: 6},
				},
				NewText: "?",
			},
			expected: []string{"caf\u0065\u0301?"},
		},
		{
			name: "Skin tone modifier - edit after modified emoji",
			// 👋🏽 = U+1F44B U+1F3FD = 2 runes, 4 UTF-16 units, 8 bytes
			lines: []string{"hi👋🏽bye"},
			edit: protocol.TextEdit{
				Range: protocol.Range{
					// "hi" = 2, 👋🏽 = 4, total = 6 UTF-16 units
					Start: protocol.Position{Line: 0, Character: 6},
					End:   protocol.Position{Line: 0, Character: 9},
				},
				NewText: "later",
			},
			expected: []string{"hi👋🏽later"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := applyTextEdit(tt.lines, tt.edit, powernap.UTF16)
			if err != nil {
				t.Fatalf("applyTextEdit failed: %v", err)
			}
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d lines, got %d: %v", len(tt.expected), len(result), result)
				return
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("line %d: expected %q, got %q", i, tt.expected[i], result[i])
				}
			}
		})
	}
}

func TestApplyTextEdit_UTF8(t *testing.T) {
	// Test that UTF-8 offsets are used directly without conversion
	tests := []struct {
		name     string
		lines    []string
		edit     protocol.TextEdit
		expected []string
	}{
		{
			name:  "ASCII only - direct byte offset",
			lines: []string{"hello world"},
			edit: protocol.TextEdit{
				Range: protocol.Range{
					Start: protocol.Position{Line: 0, Character: 6},
					End:   protocol.Position{Line: 0, Character: 11},
				},
				NewText: "universe",
			},
			expected: []string{"hello universe"},
		},
		{
			name:  "CJK characters - byte offset used directly",
			lines: []string{`var x = "你好world"`},
			edit: protocol.TextEdit{
				Range: protocol.Range{
					// With UTF-8 encoding, position 15 is the byte offset
					Start: protocol.Position{Line: 0, Character: 15},
					End:   protocol.Position{Line: 0, Character: 20},
				},
				NewText: "universe",
			},
			expected: []string{`var x = "你好universe"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := applyTextEdit(tt.lines, tt.edit, powernap.UTF8)
			if err != nil {
				t.Fatalf("applyTextEdit failed: %v", err)
			}
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d lines, got %d: %v", len(tt.expected), len(result), result)
				return
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("line %d: expected %q, got %q", i, tt.expected[i], result[i])
				}
			}
		})
	}
}

func TestRangesOverlap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		r1   protocol.Range
		r2   protocol.Range
		want bool
	}{
		{
			name: "adjacent ranges do not overlap",
			r1: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 5},
			},
			r2: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 5},
				End:   protocol.Position{Line: 0, Character: 10},
			},
			want: false,
		},
		{
			name: "overlapping ranges",
			r1: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 8},
			},
			r2: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 5},
				End:   protocol.Position{Line: 0, Character: 10},
			},
			want: true,
		},
		{
			name: "non-overlapping with gap",
			r1: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 0},
				End:   protocol.Position{Line: 0, Character: 3},
			},
			r2: protocol.Range{
				Start: protocol.Position{Line: 0, Character: 7},
				End:   protocol.Position{Line: 0, Character: 10},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := rangesOverlap(tt.r1, tt.r2)
			require.Equal(t, tt.want, got, "rangesOverlap(r1, r2)")
			// Overlap should be symmetric
			got2 := rangesOverlap(tt.r2, tt.r1)
			require.Equal(t, tt.want, got2, "rangesOverlap(r2, r1) symmetry")
		})
	}
}

func TestApplyTextEdit_UTF32(t *testing.T) {
	t.Parallel()

	lines := []string{"\u4f60\u597dworld"}
	edit := protocol.TextEdit{
		Range: protocol.Range{
			// UTF-32: codepoints, so "\u4f60\u597d" (2 codepoints) then "world" (5) = 7 total.
			Start: protocol.Position{Line: 0, Character: 2},
			End:   protocol.Position{Line: 0, Character: 7},
		},
		NewText: "universe",
	}

	result, err := applyTextEdit(lines, edit, powernap.UTF32)
	require.NoError(t, err)
	require.Equal(t, []string{"\u4f60\u597duniverse"}, result)
}

func TestUtf32ToByteOffset(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		lineText string
		offset   uint32
		want     int
	}{
		{"ASCII", "hello", 3, 3},
		{"offset zero", "hello", 0, 0},
		{"offset beyond end clamps to length", "hi", 100, 2},
		{"empty string", "", 0, 0},
		{"multi-byte codepoint counted as one", "h\u00e9llo", 2, 3},
		{"CJK codepoints", "\u4f60\u597dworld", 2, 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, utf32ToByteOffset(tt.lineText, tt.offset))
		})
	}
}

func TestApplyTextEdits(t *testing.T) {
	t.Parallel()

	t.Run("single edit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "a.txt")
		require.NoError(t, os.WriteFile(file, []byte("hello world\n"), 0o644))
		uri := protocol.URIFromPath(file)

		err := applyTextEdits(uri, []protocol.TextEdit{
			{
				Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 6}, End: protocol.Position{Line: 0, Character: 11}},
				NewText: "there",
			},
		}, powernap.UTF16)
		require.NoError(t, err)

		got, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Equal(t, "hello there\n", string(got))
	})

	t.Run("multiple edits apply without shifting offsets", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "a.txt")
		require.NoError(t, os.WriteFile(file, []byte("line one\nline two\nline three\n"), 0o644))
		uri := protocol.URIFromPath(file)

		err := applyTextEdits(uri, []protocol.TextEdit{
			{
				Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 5}, End: protocol.Position{Line: 0, Character: 8}},
				NewText: "1",
			},
			{
				Range:   protocol.Range{Start: protocol.Position{Line: 2, Character: 5}, End: protocol.Position{Line: 2, Character: 10}},
				NewText: "3",
			},
		}, powernap.UTF16)
		require.NoError(t, err)

		got, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Equal(t, "line 1\nline two\nline 3\n", string(got))
	})

	t.Run("overlapping edits are rejected", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "a.txt")
		require.NoError(t, os.WriteFile(file, []byte("hello world\n"), 0o644))
		uri := protocol.URIFromPath(file)

		err := applyTextEdits(uri, []protocol.TextEdit{
			{Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 8}}, NewText: "a"},
			{Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 5}, End: protocol.Position{Line: 0, Character: 11}}, NewText: "b"},
		}, powernap.UTF16)
		require.Error(t, err)
		require.Contains(t, err.Error(), "overlapping edits")
	})

	t.Run("preserves CRLF line endings", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "a.txt")
		require.NoError(t, os.WriteFile(file, []byte("hello\r\nworld\r\n"), 0o644))
		uri := protocol.URIFromPath(file)

		err := applyTextEdits(uri, []protocol.TextEdit{
			{Range: protocol.Range{Start: protocol.Position{Line: 1, Character: 0}, End: protocol.Position{Line: 1, Character: 5}}, NewText: "there"},
		}, powernap.UTF16)
		require.NoError(t, err)

		got, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Equal(t, "hello\r\nthere\r\n", string(got))
	})

	t.Run("missing trailing newline is preserved", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "a.txt")
		require.NoError(t, os.WriteFile(file, []byte("hello world"), 0o644))
		uri := protocol.URIFromPath(file)

		err := applyTextEdits(uri, []protocol.TextEdit{
			{Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 6}, End: protocol.Position{Line: 0, Character: 11}}, NewText: "there"},
		}, powernap.UTF16)
		require.NoError(t, err)

		got, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Equal(t, "hello there", string(got))
	})

	t.Run("deletion removes text", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "a.txt")
		require.NoError(t, os.WriteFile(file, []byte("hello world\n"), 0o644))
		uri := protocol.URIFromPath(file)

		err := applyTextEdits(uri, []protocol.TextEdit{
			{Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 5}, End: protocol.Position{Line: 0, Character: 11}}, NewText: ""},
		}, powernap.UTF16)
		require.NoError(t, err)

		got, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Equal(t, "hello\n", string(got))
	})

	t.Run("missing file returns an error", func(t *testing.T) {
		t.Parallel()
		uri := protocol.URIFromPath(filepath.Join(t.TempDir(), "missing.txt"))
		err := applyTextEdits(uri, []protocol.TextEdit{
			{Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 0}}, NewText: "x"},
		}, powernap.UTF16)
		require.Error(t, err)
	})

	t.Run("invalid uri returns an error", func(t *testing.T) {
		t.Parallel()
		err := applyTextEdits(protocol.DocumentURI("file://%zz"), []protocol.TextEdit{}, powernap.UTF16)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid URI")
	})
}

func TestApplyDocumentChange_CreateFile(t *testing.T) {
	t.Parallel()

	t.Run("creates a new empty file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "new.txt")
		change := protocol.DocumentChange{
			CreateFile: &protocol.CreateFile{
				Kind: "create",
				URI:  protocol.URIFromPath(file),
			},
		}
		require.NoError(t, applyDocumentChange(change, powernap.UTF16))
		_, err := os.Stat(file)
		require.NoError(t, err)
	})

	t.Run("ignoreIfExists skips an existing file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "existing.txt")
		require.NoError(t, os.WriteFile(file, []byte("keep me"), 0o644))

		change := protocol.DocumentChange{
			CreateFile: &protocol.CreateFile{
				Kind:    "create",
				URI:     protocol.URIFromPath(file),
				Options: &protocol.CreateFileOptions{IgnoreIfExists: true},
			},
		}
		require.NoError(t, applyDocumentChange(change, powernap.UTF16))

		content, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Equal(t, "keep me", string(content))
	})

	t.Run("overwrite truncates an existing file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "existing.txt")
		require.NoError(t, os.WriteFile(file, []byte("keep me"), 0o644))

		change := protocol.DocumentChange{
			CreateFile: &protocol.CreateFile{
				Kind:    "create",
				URI:     protocol.URIFromPath(file),
				Options: &protocol.CreateFileOptions{Overwrite: true},
			},
		}
		require.NoError(t, applyDocumentChange(change, powernap.UTF16))

		content, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Empty(t, string(content))
	})
}

func TestApplyDocumentChange_DeleteFile(t *testing.T) {
	t.Parallel()

	t.Run("deletes a single file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "gone.txt")
		require.NoError(t, os.WriteFile(file, []byte("bye"), 0o644))

		change := protocol.DocumentChange{
			DeleteFile: &protocol.DeleteFile{Kind: "delete", URI: protocol.URIFromPath(file)},
		}
		require.NoError(t, applyDocumentChange(change, powernap.UTF16))

		_, err := os.Stat(file)
		require.True(t, os.IsNotExist(err))
	})

	t.Run("recursive deletes a directory tree", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		sub := filepath.Join(dir, "sub")
		require.NoError(t, os.Mkdir(sub, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(sub, "child.txt"), []byte("x"), 0o644))

		change := protocol.DocumentChange{
			DeleteFile: &protocol.DeleteFile{
				Kind:    "delete",
				URI:     protocol.URIFromPath(sub),
				Options: &protocol.DeleteFileOptions{Recursive: true},
			},
		}
		require.NoError(t, applyDocumentChange(change, powernap.UTF16))

		_, err := os.Stat(sub)
		require.True(t, os.IsNotExist(err))
	})

	t.Run("non-recursive delete of a non-empty directory fails", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		sub := filepath.Join(dir, "sub")
		require.NoError(t, os.Mkdir(sub, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(sub, "child.txt"), []byte("x"), 0o644))

		change := protocol.DocumentChange{
			DeleteFile: &protocol.DeleteFile{Kind: "delete", URI: protocol.URIFromPath(sub)},
		}
		err := applyDocumentChange(change, powernap.UTF16)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to delete file")
	})
}

func TestApplyDocumentChange_RenameFile(t *testing.T) {
	t.Parallel()

	t.Run("renames a file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		oldPath := filepath.Join(dir, "old.txt")
		newPath := filepath.Join(dir, "new.txt")
		require.NoError(t, os.WriteFile(oldPath, []byte("content"), 0o644))

		change := protocol.DocumentChange{
			RenameFile: &protocol.RenameFile{
				Kind:   "rename",
				OldURI: protocol.URIFromPath(oldPath),
				NewURI: protocol.URIFromPath(newPath),
			},
		}
		require.NoError(t, applyDocumentChange(change, powernap.UTF16))

		_, err := os.Stat(oldPath)
		require.True(t, os.IsNotExist(err))
		content, err := os.ReadFile(newPath)
		require.NoError(t, err)
		require.Equal(t, "content", string(content))
	})

	t.Run("refuses to overwrite when Overwrite is explicitly false", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		oldPath := filepath.Join(dir, "old.txt")
		newPath := filepath.Join(dir, "new.txt")
		require.NoError(t, os.WriteFile(oldPath, []byte("old"), 0o644))
		require.NoError(t, os.WriteFile(newPath, []byte("new"), 0o644))

		change := protocol.DocumentChange{
			RenameFile: &protocol.RenameFile{
				Kind:    "rename",
				OldURI:  protocol.URIFromPath(oldPath),
				NewURI:  protocol.URIFromPath(newPath),
				Options: &protocol.RenameFileOptions{Overwrite: false},
			},
		}
		err := applyDocumentChange(change, powernap.UTF16)
		require.Error(t, err)
		require.Contains(t, err.Error(), "already exists")

		content, err := os.ReadFile(newPath)
		require.NoError(t, err)
		require.Equal(t, "new", string(content), "target must be untouched")
	})

	// A nil Options is not the same as Overwrite:false: the "already
	// exists" guard only runs when Options is set, so an omitted Options
	// silently allows os.Rename to overwrite the target.
	t.Run("nil options silently allows overwrite", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		oldPath := filepath.Join(dir, "old.txt")
		newPath := filepath.Join(dir, "new.txt")
		require.NoError(t, os.WriteFile(oldPath, []byte("old"), 0o644))
		require.NoError(t, os.WriteFile(newPath, []byte("new"), 0o644))

		change := protocol.DocumentChange{
			RenameFile: &protocol.RenameFile{
				Kind:   "rename",
				OldURI: protocol.URIFromPath(oldPath),
				NewURI: protocol.URIFromPath(newPath),
			},
		}
		require.NoError(t, applyDocumentChange(change, powernap.UTF16))

		content, err := os.ReadFile(newPath)
		require.NoError(t, err)
		require.Equal(t, "old", string(content))
	})

	t.Run("overwrite replaces an existing target", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		oldPath := filepath.Join(dir, "old.txt")
		newPath := filepath.Join(dir, "new.txt")
		require.NoError(t, os.WriteFile(oldPath, []byte("old"), 0o644))
		require.NoError(t, os.WriteFile(newPath, []byte("new"), 0o644))

		change := protocol.DocumentChange{
			RenameFile: &protocol.RenameFile{
				Kind:    "rename",
				OldURI:  protocol.URIFromPath(oldPath),
				NewURI:  protocol.URIFromPath(newPath),
				Options: &protocol.RenameFileOptions{Overwrite: true},
			},
		}
		require.NoError(t, applyDocumentChange(change, powernap.UTF16))

		content, err := os.ReadFile(newPath)
		require.NoError(t, err)
		require.Equal(t, "old", string(content))
	})
}

func TestApplyDocumentChange_TextDocumentEdit(t *testing.T) {
	t.Parallel()

	t.Run("delegates to applyTextEdits", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "a.txt")
		require.NoError(t, os.WriteFile(file, []byte("hello world\n"), 0o644))

		change := protocol.DocumentChange{
			TextDocumentEdit: &protocol.TextDocumentEdit{
				TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: protocol.URIFromPath(file)},
				},
				Edits: []protocol.Or_TextDocumentEdit_edits_Elem{
					{Value: protocol.TextEdit{
						Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 6}, End: protocol.Position{Line: 0, Character: 11}},
						NewText: "there",
					}},
				},
			},
		}
		require.NoError(t, applyDocumentChange(change, powernap.UTF16))

		content, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Equal(t, "hello there\n", string(content))
	})

	t.Run("an unsupported edit element type is an error", func(t *testing.T) {
		t.Parallel()
		change := protocol.DocumentChange{
			TextDocumentEdit: &protocol.TextDocumentEdit{
				TextDocument: protocol.OptionalVersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: protocol.URIFromPath(filepath.Join(t.TempDir(), "a.txt"))},
				},
				Edits: []protocol.Or_TextDocumentEdit_edits_Elem{
					{Value: "not-a-text-edit"},
				},
			},
		}
		err := applyDocumentChange(change, powernap.UTF16)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid edit type")
	})
}

func TestApplyDocumentChange_InvalidURI(t *testing.T) {
	t.Parallel()

	change := protocol.DocumentChange{
		CreateFile: &protocol.CreateFile{Kind: "create", URI: protocol.DocumentURI("file://%zz")},
	}
	err := applyDocumentChange(change, powernap.UTF16)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid URI")
}

func TestApplyWorkspaceEdit(t *testing.T) {
	t.Parallel()

	t.Run("applies changes map", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "a.txt")
		require.NoError(t, os.WriteFile(file, []byte("hello world\n"), 0o644))

		edit := protocol.WorkspaceEdit{
			Changes: map[protocol.DocumentURI][]protocol.TextEdit{
				protocol.URIFromPath(file): {
					{
						Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 6}, End: protocol.Position{Line: 0, Character: 11}},
						NewText: "there",
					},
				},
			},
		}
		require.NoError(t, ApplyWorkspaceEdit(edit, powernap.UTF16))

		content, err := os.ReadFile(file)
		require.NoError(t, err)
		require.Equal(t, "hello there\n", string(content))
	})

	t.Run("applies document changes", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		file := filepath.Join(dir, "new.txt")

		edit := protocol.WorkspaceEdit{
			DocumentChanges: []protocol.DocumentChange{
				{CreateFile: &protocol.CreateFile{Kind: "create", URI: protocol.URIFromPath(file)}},
			},
		}
		require.NoError(t, ApplyWorkspaceEdit(edit, powernap.UTF16))

		_, err := os.Stat(file)
		require.NoError(t, err)
	})

	t.Run("propagates a Changes failure wrapped with context", func(t *testing.T) {
		t.Parallel()
		edit := protocol.WorkspaceEdit{
			Changes: map[protocol.DocumentURI][]protocol.TextEdit{
				protocol.URIFromPath(filepath.Join(t.TempDir(), "missing.txt")): {
					{Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 0}}, NewText: "x"},
				},
			},
		}
		err := ApplyWorkspaceEdit(edit, powernap.UTF16)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to apply text edits")
	})

	t.Run("propagates a DocumentChanges failure wrapped with context", func(t *testing.T) {
		t.Parallel()
		edit := protocol.WorkspaceEdit{
			DocumentChanges: []protocol.DocumentChange{
				{DeleteFile: &protocol.DeleteFile{Kind: "delete", URI: protocol.URIFromPath(filepath.Join(t.TempDir(), "missing.txt"))}},
			},
		}
		err := ApplyWorkspaceEdit(edit, powernap.UTF16)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to apply document change")
	})

	t.Run("empty edit is a no-op", func(t *testing.T) {
		t.Parallel()
		require.NoError(t, ApplyWorkspaceEdit(protocol.WorkspaceEdit{}, powernap.UTF16))
	})
}
