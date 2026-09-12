package permission

import (
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

func TestFilesystemAllowPaths_EditRuleAddsReadWrite(t *testing.T) {
	t.Parallel()

	readOnlyDirs, readWriteDirs, readOnlyFiles, readWriteFiles := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "edit", Pattern: "secrets/*"},
	}, "/work")

	require.Empty(t, readOnlyDirs)
	require.Empty(t, readOnlyFiles)
	require.Empty(t, readWriteFiles)
	require.Equal(t, []string{filepath.Join("/work", "secrets")}, readWriteDirs)
}

func TestFilesystemAllowPaths_ReadAndListRulesAddReadOnly(t *testing.T) {
	t.Parallel()

	readOnlyDirs, readWriteDirs, _, _ := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "read", Pattern: "docs/**"},
		{Action: RuleAllow, Tool: "list", Pattern: "logs/*"},
	}, "/work")

	require.Equal(t, []string{filepath.Join("/work", "docs"), filepath.Join("/work", "logs")}, readOnlyDirs)
	require.Empty(t, readWriteDirs)
}

func TestFilesystemAllowPaths_BuiltinToolNames(t *testing.T) {
	t.Parallel()

	readOnlyDirs, readWriteDirs, _, _ := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: toolnames.Write, Pattern: "out/*"},
		{Action: RuleAllow, Tool: toolnames.Edit, Pattern: "src/*"},
		{Action: RuleAllow, Tool: toolnames.MultiEdit, Pattern: "gen/*"},
		{Action: RuleAllow, Tool: toolnames.View, Pattern: "ro-view/*"},
		{Action: RuleAllow, Tool: toolnames.Glob, Pattern: "ro-glob/*"},
		{Action: RuleAllow, Tool: toolnames.Grep, Pattern: "ro-grep/*"},
		{Action: RuleAllow, Tool: toolnames.LS, Pattern: "ro-ls/*"},
	}, "/work")

	require.Equal(t, []string{filepath.Join("/work", "ro-view"), filepath.Join("/work", "ro-glob"), filepath.Join("/work", "ro-grep"), filepath.Join("/work", "ro-ls")}, readOnlyDirs)
	require.Equal(t, []string{filepath.Join("/work", "out"), filepath.Join("/work", "src"), filepath.Join("/work", "gen")}, readWriteDirs)
}

func TestFilesystemAllowPaths_WriteAliasAddsReadWrite(t *testing.T) {
	t.Parallel()

	_, readWriteDirs, _, _ := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "write", Pattern: "out/*"},
	}, "/work")

	require.Equal(t, []string{filepath.Join("/work", "out")}, readWriteDirs)
}

func TestFilesystemAllowPaths_SkipsNonFilesystemTools(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"execute", "network", "mcp", toolnames.Bash, toolnames.Fetch} {
		readOnlyDirs, readWriteDirs, readOnlyFiles, readWriteFiles := FilesystemAllowPaths([]Rule{
			{Action: RuleAllow, Tool: tool, Pattern: "some/dir/**"},
		}, "/work")
		require.Emptyf(t, readOnlyDirs, "tool %q should not contribute a read-only dir", tool)
		require.Emptyf(t, readWriteDirs, "tool %q should not contribute a read-write dir", tool)
		require.Emptyf(t, readOnlyFiles, "tool %q should not contribute a read-only file", tool)
		require.Emptyf(t, readWriteFiles, "tool %q should not contribute a read-write file", tool)
	}
}

func TestFilesystemAllowPaths_SkipsEmptyOrWildcardTool(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"", "*"} {
		readOnlyDirs, readWriteDirs, readOnlyFiles, readWriteFiles := FilesystemAllowPaths([]Rule{
			{Action: RuleAllow, Tool: tool, Pattern: "some/dir/**"},
		}, "/work")
		require.Emptyf(t, readOnlyDirs, "tool %q should not contribute a read-only dir", tool)
		require.Emptyf(t, readWriteDirs, "tool %q should not contribute a read-write dir", tool)
		require.Emptyf(t, readOnlyFiles, "tool %q should not contribute a read-only file", tool)
		require.Emptyf(t, readWriteFiles, "tool %q should not contribute a read-write file", tool)
	}
}

func TestFilesystemAllowPaths_SkipsPatternsWithNoLiteralDirectory(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{"", "*", "**", "**/vendor/**", "*.env"} {
		readOnlyDirs, readWriteDirs, readOnlyFiles, readWriteFiles := FilesystemAllowPaths([]Rule{
			{Action: RuleAllow, Tool: "edit", Pattern: pattern},
		}, "/work")
		require.Emptyf(t, readWriteDirs, "pattern %q should not contribute a directory", pattern)
		require.Empty(t, readOnlyDirs)
		require.Emptyf(t, readWriteFiles, "pattern %q should not contribute a file", pattern)
		require.Empty(t, readOnlyFiles)
	}
}

func TestFilesystemAllowPaths_SkipsFilesystemRoot(t *testing.T) {
	t.Parallel()

	readOnlyDirs, readWriteDirs, readOnlyFiles, readWriteFiles := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "edit", Pattern: "/**"},
	}, "/work")

	require.Empty(t, readOnlyDirs)
	require.Empty(t, readWriteDirs)
	require.Empty(t, readOnlyFiles)
	require.Empty(t, readWriteFiles)
}

func TestFilesystemAllowPaths_SkipsDenyAndAskRules(t *testing.T) {
	t.Parallel()

	readOnlyDirs, readWriteDirs, _, _ := FilesystemAllowPaths([]Rule{
		{Action: RuleDeny, Tool: "edit", Pattern: "outside/**"},
		{Action: RuleAsk, Tool: "edit", Pattern: "outside/**"},
	}, "/work")

	require.Empty(t, readOnlyDirs)
	require.Empty(t, readWriteDirs)
}

func TestFilesystemAllowPaths_AbsolutePatternIgnoresCwd(t *testing.T) {
	t.Parallel()

	readOnlyDirs, _, _, _ := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "read", Pattern: "/etc/angela/**"},
	}, "/work")

	require.Equal(t, []string{filepath.Clean("/etc/angela")}, readOnlyDirs)
}

// TestFilesystemAllowPaths_LiteralFileGrantsExactFileOnly pins the
// fix for a literal file pattern, i.e. one with no glob wildcard
// anywhere: it must contribute the exact file, not its parent
// directory, since a caller like sandboxConfigFromFlags feeds these
// straight into an OS-level sandbox where a directory grant would
// cover every other file alongside it too.
func TestFilesystemAllowPaths_LiteralFileGrantsExactFileOnly(t *testing.T) {
	t.Parallel()

	readOnlyDirs, readWriteDirs, _, readWriteFiles := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "edit", Pattern: "secrets/key.txt"},
	}, "/work")

	require.Empty(t, readOnlyDirs)
	require.Empty(t, readWriteDirs, "a literal file rule must not widen its whole parent directory")
	require.Equal(t, []string{filepath.Join("/work", "secrets", "key.txt")}, readWriteFiles)
}

// TestFilesystemAllowPaths_LiteralFileDoesNotCoverSiblings is the
// regression test for the vulnerability this fixes: a rule allowing
// edits to one literal file must not also cover a sibling file that
// merely lives in the same directory, which is what deriving the
// parent directory used to do.
func TestFilesystemAllowPaths_LiteralFileDoesNotCoverSiblings(t *testing.T) {
	t.Parallel()

	_, readWriteDirs, _, readWriteFiles := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "edit", Pattern: "secrets/key.txt"},
	}, "/work")

	sibling := filepath.Join("/work", "secrets", "other.txt")
	require.NotContains(t, readWriteFiles, sibling)
	require.NotContains(t, readWriteDirs, filepath.Join("/work", "secrets"),
		"granting the parent directory would implicitly cover the sibling too")
}

func TestFilesystemAllowPaths_LiteralFileWithNoDirectoryComponent(t *testing.T) {
	t.Parallel()

	_, _, readOnlyFiles, _ := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "read", Pattern: "key.txt"},
	}, "/work")

	require.Equal(t, []string{filepath.Join("/work", "key.txt")}, readOnlyFiles)
}

func TestFilesystemAllowPaths_MultipleRulesAccumulateInOrder(t *testing.T) {
	t.Parallel()

	readOnlyDirs, readWriteDirs, readOnlyFiles, readWriteFiles := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "edit", Pattern: "a/**"},
		{Action: RuleAllow, Tool: "read", Pattern: "b/**"},
		{Action: RuleAllow, Tool: "edit", Pattern: "c/**"},
		{Action: RuleAllow, Tool: "edit", Pattern: "d/key.txt"},
		{Action: RuleAllow, Tool: "read", Pattern: "e/key.txt"},
	}, "/work")

	require.Equal(t, []string{filepath.Join("/work", "b")}, readOnlyDirs)
	require.Equal(t, []string{filepath.Join("/work", "a"), filepath.Join("/work", "c")}, readWriteDirs)
	require.Equal(t, []string{filepath.Join("/work", "e", "key.txt")}, readOnlyFiles)
	require.Equal(t, []string{filepath.Join("/work", "d", "key.txt")}, readWriteFiles)
}

func TestRuleDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		cwd     string
		wantDir string
		wantOK  bool
	}{
		{"relative glob", "secrets/*", "/work", filepath.Join("/work", "secrets"), true},
		{"dot-relative glob", "./**", "/work", filepath.Join("/work", "."), true},
		{"absolute glob", "/data/**", "/work", filepath.Clean("/data"), true},
		{"empty pattern", "", "/work", "", false},
		{"star only", "*", "/work", "", false},
		{"leading wildcard", "**/x", "/work", "", false},
		{"root glob", "/**", "/work", "", false},
		{"literal file is not a directory", "secrets/key.txt", "/work", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir, ok := ruleDir(tt.pattern, tt.cwd)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.Equal(t, tt.wantDir, dir)
			}
		})
	}
}

func TestRuleFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pattern  string
		cwd      string
		wantFile string
		wantOK   bool
	}{
		{"literal relative file", "secrets/key.txt", "/work", filepath.Join("/work", "secrets", "key.txt"), true},
		{"literal file with no directory component", "key.txt", "/work", filepath.Join("/work", "key.txt"), true},
		{"literal absolute file", "/etc/angela/key.txt", "/work", filepath.Clean("/etc/angela/key.txt"), true},
		{"empty pattern", "", "/work", "", false},
		{"wildcard pattern", "secrets/*", "/work", "", false},
		{"wildcard basename", "*.env", "/work", "", false},
		{"root", "/", "/work", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			file, ok := ruleFile(tt.pattern, tt.cwd)
			require.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				require.Equal(t, tt.wantFile, file)
			}
		})
	}
}

func TestHasGlobMeta(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		want    bool
	}{
		{"plain literal", "secrets/key.txt", false},
		{"no directory", "key.txt", false},
		{"star", "secrets/*", true},
		{"double star", "secrets/**", true},
		{"question mark", "file?.txt", true},
		{"bracket class", "file[0-9].txt", true},
		{"brace group", "file{a,b}.txt", true},
		{"escaped character", `file\*.txt`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, hasGlobMeta(tt.pattern))
		})
	}
}
