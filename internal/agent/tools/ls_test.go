package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

func TestCreateFileTree(t *testing.T) {
	t.Parallel()

	t.Run("files and directories at multiple depths", func(t *testing.T) {
		t.Parallel()
		sep := string(filepath.Separator)
		root := sep + "proj"
		paths := []string{
			root + sep + "a" + sep,
			root + sep + "a" + sep + "one.go",
			root + sep + "a" + sep + "b" + sep,
			root + sep + "a" + sep + "b" + sep + "two.go",
			root + sep + "top.txt",
		}

		tree := createFileTree(paths, root)
		require.Len(t, tree, 2, "expected top-level entries: a/ and top.txt")

		var dirA, fileTop *TreeNode
		for _, n := range tree {
			switch n.Name {
			case "a":
				dirA = n
			case "top.txt":
				fileTop = n
			}
		}
		require.NotNil(t, dirA)
		require.NotNil(t, fileTop)
		require.Equal(t, NodeTypeDirectory, dirA.Type)
		require.Equal(t, NodeTypeFile, fileTop.Type)
		require.Len(t, dirA.Children, 2, "a/ should contain one.go and b/")

		var fileOne, dirB *TreeNode
		for _, n := range dirA.Children {
			switch n.Name {
			case "one.go":
				fileOne = n
			case "b":
				dirB = n
			}
		}
		require.NotNil(t, fileOne)
		require.Equal(t, NodeTypeFile, fileOne.Type)
		require.NotNil(t, dirB)
		require.Equal(t, NodeTypeDirectory, dirB.Type)
		require.Len(t, dirB.Children, 1)
		require.Equal(t, "two.go", dirB.Children[0].Name)
		require.Equal(t, NodeTypeFile, dirB.Children[0].Type)
	})

	t.Run("empty input yields empty tree", func(t *testing.T) {
		t.Parallel()
		require.Empty(t, createFileTree(nil, "/proj"))
	})

	t.Run("path equal to root is skipped", func(t *testing.T) {
		t.Parallel()
		tree := createFileTree([]string{"/proj"}, "/proj")
		require.Empty(t, tree)
	})
}

func TestPrintTree(t *testing.T) {
	t.Parallel()

	sep := string(filepath.Separator)
	root := sep + "proj"
	tree := createFileTree([]string{
		root + sep + "a" + sep,
		root + sep + "a" + sep + "one.go",
		root + sep + "top.txt",
	}, root)

	got := printTree(tree, root)
	want := "- /proj/\n" +
		"  - a/\n" +
		"    - one.go\n" +
		"  - top.txt\n"
	require.Equal(t, want, got)
}

func TestPrintTreeRootAlreadyHasTrailingSlash(t *testing.T) {
	t.Parallel()

	got := printTree(nil, "/proj/")
	require.Equal(t, "- /proj/\n", got)
}

func TestListDirectoryTreeErrorsOnMissingPath(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, _, err := ListDirectoryTree(missing, LSParams{}, config.ToolLs{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "path does not exist")
}

func TestListDirectoryTreeListsNestedFilesAndHiddenDotfiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	mustWrite := func(rel string) {
		full := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte("x"), 0o644))
	}
	mustWrite("top.go")
	mustWrite("sub/nested.go")
	mustWrite(".env")

	output, metadata, err := ListDirectoryTree(root, LSParams{}, config.ToolLs{})
	require.NoError(t, err)
	require.False(t, metadata.Truncated)
	require.Equal(t, 4, metadata.NumberOfFiles, "top.go, .env, sub/, sub/nested.go")
	require.Contains(t, output, "top.go")
	require.Contains(t, output, "sub/")
	require.Contains(t, output, "nested.go")
	require.Contains(t, output, ".env", "ls does not hide arbitrary dotfiles by default")
}

func TestListDirectoryTreeRespectsIgnorePatterns(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "keep.go"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "skip.log"), []byte("x"), 0o644))

	output, _, err := ListDirectoryTree(root, LSParams{Ignore: []string{"*.log"}}, config.ToolLs{})
	require.NoError(t, err)
	require.Contains(t, output, "keep.go")
	require.NotContains(t, output, "skip.log")
}

func TestListDirectoryTreeTruncatesAtMaxItems(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for i := range 200 {
		dir := filepath.Join(root, "d"+strconv.Itoa(i))
		require.NoError(t, os.Mkdir(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644))
	}

	maxItems := 3
	output, metadata, err := ListDirectoryTree(root, LSParams{}, config.ToolLs{MaxItems: &maxItems})
	require.NoError(t, err)
	require.True(t, metadata.Truncated)
	require.LessOrEqual(t, metadata.NumberOfFiles, maxItems)
	require.Contains(t, output, "There are more than 3 files in the directory")
}

func TestListDirectoryTreeAnnotatesConfiguredDepth(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "a", "b"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "a", "b", "deep.go"), []byte("x"), 0o644))

	maxDepth := 1
	output, _, err := ListDirectoryTree(root, LSParams{}, config.ToolLs{MaxDepth: &maxDepth})
	require.NoError(t, err)
	require.Contains(t, output, "The directory tree is shown up to a depth of 1")
}

func TestNewLsToolEndToEnd(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.go"), []byte("x"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "b.go"), []byte("x"), 0o644))

	tool := NewLsTool(root, config.ToolLs{})

	t.Run("defaults to working directory", func(t *testing.T) {
		t.Parallel()
		input, err := json.Marshal(LSParams{})
		require.NoError(t, err)
		resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.LS, Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		require.Contains(t, resp.Content, "a.go")
		require.Contains(t, resp.Content, "sub/")
		require.NotEmpty(t, resp.Metadata)
	})

	t.Run("explicit relative path scopes the listing", func(t *testing.T) {
		t.Parallel()
		input, err := json.Marshal(LSParams{Path: "sub"})
		require.NoError(t, err)
		resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "2", Name: toolnames.LS, Input: string(input)})
		require.NoError(t, err)
		require.False(t, resp.IsError, resp.Content)
		require.Contains(t, resp.Content, "b.go")
		require.NotContains(t, resp.Content, "a.go")
	})

	t.Run("missing directory produces an error response", func(t *testing.T) {
		t.Parallel()
		input, err := json.Marshal(LSParams{Path: filepath.Join(root, "does-not-exist")})
		require.NoError(t, err)
		resp, err := tool.Run(context.Background(), fantasy.ToolCall{ID: "3", Name: toolnames.LS, Input: string(input)})
		require.NoError(t, err)
		require.True(t, resp.IsError)
		require.Contains(t, resp.Content, "path does not exist")
	})
}
