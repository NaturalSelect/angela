package permission

import (
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

func TestFilesystemAllowPaths_EditRuleAddsReadWrite(t *testing.T) {
	t.Parallel()

	readOnly, readWrite := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "edit", Pattern: "secrets/*"},
	}, "/work")

	require.Empty(t, readOnly)
	require.Equal(t, []string{"/work/secrets"}, readWrite)
}

func TestFilesystemAllowPaths_ReadAndListRulesAddReadOnly(t *testing.T) {
	t.Parallel()

	readOnly, readWrite := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "read", Pattern: "docs/**"},
		{Action: RuleAllow, Tool: "list", Pattern: "logs/*"},
	}, "/work")

	require.Equal(t, []string{"/work/docs", "/work/logs"}, readOnly)
	require.Empty(t, readWrite)
}

func TestFilesystemAllowPaths_BuiltinToolNames(t *testing.T) {
	t.Parallel()

	readOnly, readWrite := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: toolnames.Write, Pattern: "out/*"},
		{Action: RuleAllow, Tool: toolnames.Edit, Pattern: "src/*"},
		{Action: RuleAllow, Tool: toolnames.MultiEdit, Pattern: "gen/*"},
		{Action: RuleAllow, Tool: toolnames.View, Pattern: "ro-view/*"},
		{Action: RuleAllow, Tool: toolnames.Glob, Pattern: "ro-glob/*"},
		{Action: RuleAllow, Tool: toolnames.Grep, Pattern: "ro-grep/*"},
		{Action: RuleAllow, Tool: toolnames.LS, Pattern: "ro-ls/*"},
	}, "/work")

	require.Equal(t, []string{"/work/ro-view", "/work/ro-glob", "/work/ro-grep", "/work/ro-ls"}, readOnly)
	require.Equal(t, []string{"/work/out", "/work/src", "/work/gen"}, readWrite)
}

func TestFilesystemAllowPaths_WriteAliasAddsReadWrite(t *testing.T) {
	t.Parallel()

	_, readWrite := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "write", Pattern: "out/*"},
	}, "/work")

	require.Equal(t, []string{"/work/out"}, readWrite)
}

func TestFilesystemAllowPaths_SkipsNonFilesystemTools(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"execute", "network", "mcp", toolnames.Bash, toolnames.Fetch} {
		readOnly, readWrite := FilesystemAllowPaths([]Rule{
			{Action: RuleAllow, Tool: tool, Pattern: "some/dir/**"},
		}, "/work")
		require.Emptyf(t, readOnly, "tool %q should not contribute a read-only dir", tool)
		require.Emptyf(t, readWrite, "tool %q should not contribute a read-write dir", tool)
	}
}

func TestFilesystemAllowPaths_SkipsEmptyOrWildcardTool(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"", "*"} {
		readOnly, readWrite := FilesystemAllowPaths([]Rule{
			{Action: RuleAllow, Tool: tool, Pattern: "some/dir/**"},
		}, "/work")
		require.Emptyf(t, readOnly, "tool %q should not contribute a read-only dir", tool)
		require.Emptyf(t, readWrite, "tool %q should not contribute a read-write dir", tool)
	}
}

func TestFilesystemAllowPaths_SkipsPatternsWithNoLiteralDirectory(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{"", "*", "**", "**/vendor/**", "*.env"} {
		readOnly, readWrite := FilesystemAllowPaths([]Rule{
			{Action: RuleAllow, Tool: "edit", Pattern: pattern},
		}, "/work")
		require.Emptyf(t, readWrite, "pattern %q should not contribute a directory", pattern)
		require.Empty(t, readOnly)
	}
}

func TestFilesystemAllowPaths_SkipsFilesystemRoot(t *testing.T) {
	t.Parallel()

	readOnly, readWrite := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "edit", Pattern: "/**"},
	}, "/work")

	require.Empty(t, readOnly)
	require.Empty(t, readWrite)
}

func TestFilesystemAllowPaths_SkipsDenyAndAskRules(t *testing.T) {
	t.Parallel()

	readOnly, readWrite := FilesystemAllowPaths([]Rule{
		{Action: RuleDeny, Tool: "edit", Pattern: "outside/**"},
		{Action: RuleAsk, Tool: "edit", Pattern: "outside/**"},
	}, "/work")

	require.Empty(t, readOnly)
	require.Empty(t, readWrite)
}

func TestFilesystemAllowPaths_AbsolutePatternIgnoresCwd(t *testing.T) {
	t.Parallel()

	readOnly, _ := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "read", Pattern: "/etc/angela/**"},
	}, "/work")

	require.Equal(t, []string{"/etc/angela"}, readOnly)
}

func TestFilesystemAllowPaths_LiteralFileUsesParentDir(t *testing.T) {
	t.Parallel()

	_, readWrite := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "edit", Pattern: "secrets/key.txt"},
	}, "/work")

	require.Equal(t, []string{"/work/secrets"}, readWrite)
}

func TestFilesystemAllowPaths_MultipleRulesAccumulateInOrder(t *testing.T) {
	t.Parallel()

	readOnly, readWrite := FilesystemAllowPaths([]Rule{
		{Action: RuleAllow, Tool: "edit", Pattern: "a/**"},
		{Action: RuleAllow, Tool: "read", Pattern: "b/**"},
		{Action: RuleAllow, Tool: "edit", Pattern: "c/**"},
	}, "/work")

	require.Equal(t, []string{"/work/b"}, readOnly)
	require.Equal(t, []string{"/work/a", "/work/c"}, readWrite)
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
		{"dot-relative glob", "./**", "/work", "/work", true},
		{"absolute glob", "/data/**", "/work", "/data", true},
		{"empty pattern", "", "/work", "", false},
		{"star only", "*", "/work", "", false},
		{"leading wildcard", "**/x", "/work", "", false},
		{"root glob", "/**", "/work", "", false},
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
