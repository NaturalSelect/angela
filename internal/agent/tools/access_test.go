package tools

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/filepathext"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/stretchr/testify/require"
)

// jsonPath quotes a path for a JSON literal in slash form. Windows
// absolute paths carry a volume and backslashes; slashes keep the
// literal readable and are still recognised as absolute once the volume
// is there.
func jsonPath(p string) string {
	return fmt.Sprintf("%q", filepath.ToSlash(p))
}

func TestAccessOf(t *testing.T) {
	t.Parallel()

	// The roots come from the filesystem rather than a literal so the
	// table means the same thing on Windows, where an absolute path
	// needs a volume letter and the separator is not "/".
	workDir := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "hosts")

	cases := []struct {
		name  string
		tool  string
		input string
		want  permission.Access
	}{
		{
			name:  "bash carries the command and its directory",
			tool:  BashToolName,
			input: `{"command":"go test ./...","working_dir":"sub"}`,
			want: permission.Access{
				Action:  permission.ActionExecute,
				Command: "go test ./...",
				Path:    filepath.Join(workDir, "sub"),
			},
		},
		{
			name:  "bash without a directory falls back to the working dir",
			tool:  BashToolName,
			input: `{"command":"ls"}`,
			want: permission.Access{
				Action:  permission.ActionExecute,
				Command: "ls",
				Path:    workDir,
			},
		},
		{
			name:  "edit resolves a relative path",
			tool:  EditToolName,
			input: `{"file_path":"internal/a.go","old_string":"x","new_string":"y"}`,
			want:  permission.Access{Action: permission.ActionEdit, Path: filepath.Join(workDir, "internal", "a.go")},
		},
		{
			name:  "write keeps an absolute path",
			tool:  WriteToolName,
			input: fmt.Sprintf(`{"file_path":%s,"content":"x"}`, jsonPath(outsideFile)),
			want:  permission.Access{Action: permission.ActionEdit, Path: outsideFile},
		},
		{
			name:  "multiedit is an edit",
			tool:  MultiEditToolName,
			input: `{"file_path":"a.go","edits":[]}`,
			want:  permission.Access{Action: permission.ActionEdit, Path: filepath.Join(workDir, "a.go")},
		},
		{
			name:  "replace symbol is an edit",
			tool:  ReplaceSymbolToolName,
			input: `{"symbol":"F","file_path":"a.go"}`,
			want:  permission.Access{Action: permission.ActionEdit, Path: filepath.Join(workDir, "a.go")},
		},
		{
			name:  "rename is an edit over its search root",
			tool:  RenameToolName,
			input: `{"symbol":"F","new_name":"G","path":"internal"}`,
			want:  permission.Access{Action: permission.ActionEdit, Path: filepath.Join(workDir, "internal")},
		},
		{
			name:  "view is a read",
			tool:  ViewToolName,
			input: `{"file_path":"a.go"}`,
			want:  permission.Access{Action: permission.ActionRead, Path: filepath.Join(workDir, "a.go")},
		},
		{
			name:  "ls is a list",
			tool:  LSToolName,
			input: `{"path":"internal"}`,
			want:  permission.Access{Action: permission.ActionList, Path: filepath.Join(workDir, "internal")},
		},
		{
			name:  "glob folds the literal head of its pattern into the root",
			tool:  GlobToolName,
			input: `{"pattern":"internal/**/*.go"}`,
			want:  permission.Access{Action: permission.ActionList, Path: filepath.Join(workDir, "internal")},
		},
		{
			name:  "glob cannot climb out unnoticed",
			tool:  GlobToolName,
			input: `{"pattern":"../../etc/*.conf"}`,
			want:  permission.Access{Action: permission.ActionList, Path: filepath.Join(workDir, "..", "..", "etc")},
		},
		{
			name:  "grep is a read of its search root",
			tool:  GrepToolName,
			input: fmt.Sprintf(`{"pattern":"x","path":%s}`, jsonPath(outside)),
			want:  permission.Access{Action: permission.ActionRead, Path: outside},
		},
		{
			name:  "download carries both the url and the file it lands in",
			tool:  DownloadToolName,
			input: `{"url":"https://example.com/x.sh","file_path":"x.sh"}`,
			want: permission.Access{
				Action: permission.ActionNetwork,
				URL:    "https://example.com/x.sh",
				Path:   filepath.Join(workDir, "x.sh"),
			},
		},
		{
			name:  "web fetch is a network access",
			tool:  WebFetchToolName,
			input: `{"url":"https://example.com"}`,
			want:  permission.Access{Action: permission.ActionNetwork, URL: "https://example.com"},
		},
		{
			name:  "list mcp resources names its server",
			tool:  ListMCPResourcesToolName,
			input: `{"mcp_name":"docker"}`,
			want:  permission.Access{Action: permission.ActionMCP, Server: "docker"},
		},
		{
			name:  "job kill reaches only a shell this agent started",
			tool:  JobKillToolName,
			input: `{"shell_id":"abc"}`,
			want:  permission.Access{Action: permission.ActionRead},
		},
		{
			name:  "todos reads state the agent already has",
			tool:  TodosToolName,
			input: `{"todos":[]}`,
			want:  permission.Access{Action: permission.ActionRead},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := AccessOf(tc.tool, tc.input, workDir)
			require.True(t, ok)

			want := tc.want
			want.Tool = tc.tool
			require.Equal(t, want, got)
		})
	}
}

// TestAccessOfFailsClosed pins that anything the mapping does not
// understand is reported as unknown, so a caller denies it rather than
// letting it through unchecked.
func TestAccessOfFailsClosed(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		tool  string
		input string
	}{
		{"unregistered tool", "brand_new_tool", `{}`},
		{"malformed input", BashToolName, `{"command":`},
		{"wrongly typed field", ViewToolName, `{"file_path":42}`},
		{"mcp prefix without a tool name", "mcp_docker", `{}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, ok := AccessOf(tc.tool, tc.input, "/work")
			require.False(t, ok)
		})
	}
}

// TestAccessOfCoversEveryTool pins that every tool name the coordinator
// can register has a mapping. A tool missing here would be denied at
// run time, so the gap must fail the build instead.
func TestAccessOfCoversEveryTool(t *testing.T) {
	t.Parallel()

	toolNames := []string{
		agentToolName,
		AngelaInfoToolName,
		AngelaLogsToolName,
		BashToolName,
		CallHierarchyToolName,
		DefinitionToolName,
		DiagnosticsToolName,
		DownloadToolName,
		EditToolName,
		FetchToolName,
		GlobToolName,
		GrepToolName,
		JobKillToolName,
		JobOutputToolName,
		LSPRestartToolName,
		LSToolName,
		ListMCPResourcesToolName,
		MultiEditToolName,
		ProposalEditToolName,
		ProposalReadToolName,
		ProposalWriteToolName,
		QuestionToolName,
		ReadMCPResourceToolName,
		ReferencesToolName,
		RenameToolName,
		ReplaceSymbolToolName,
		SourcegraphToolName,
		SymbolsToolName,
		TodosToolName,
		ViewToolName,
		WebFetchToolName,
		WebSearchToolName,
		WriteToolName,
	}

	for _, name := range toolNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			access, ok := AccessOf(name, "{}", "/work")
			require.True(t, ok, "tool %q has no access mapping", name)
			require.Equal(t, name, access.Tool)
		})
	}
}

func TestAccessOfResolvesPathsAbsolutely(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()
	access, ok := AccessOf(ViewToolName, `{"file_path":"a/../b.go"}`, workDir)
	require.True(t, ok)
	require.True(t, filepath.IsAbs(access.Path))
	require.Equal(t, filepath.Join(workDir, "b.go"), access.Path)
}

// TestAccessOfJudgesThePathTheToolWillTouch pins that the permission
// system resolves a path the same way the tools do. The tools reach
// their files through filepathext.SmartJoin; resolving differently here
// would describe one file to the user and open another, which on
// Windows is exactly what a leading slash used to do.
func TestAccessOfJudgesThePathTheToolWillTouch(t *testing.T) {
	t.Parallel()

	workDir := t.TempDir()

	for _, input := range []string{"/etc/hosts", "sub/a.go", "a/../b.go", "."} {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			access, ok := AccessOf(ViewToolName,
				fmt.Sprintf(`{"file_path":%q}`, input), workDir)
			require.True(t, ok)
			require.Equal(t,
				filepath.Clean(filepathext.SmartJoin(workDir, input)),
				access.Path,
				"the judged path must be the one the tool opens")
		})
	}
}

// TestPreviewOfFeedsTheDialog pins that every tool whose permission
// dialog renders typed params actually receives them. The dialog type
// asserts on a concrete struct and silently renders nothing when the
// assertion fails, so a missing entry here is invisible until a user
// sees an empty prompt.
func TestPreviewOfFeedsTheDialog(t *testing.T) {
	t.Parallel()

	cases := []struct {
		tool  string
		input string
		want  any
	}{
		{BashToolName, `{"command":"rm -rf x","description":"clean"}`, BashPermissionsParams{}},
		{ViewToolName, `{"file_path":"/etc/hosts"}`, ViewPermissionsParams{}},
		{LSToolName, `{"path":"/etc"}`, LSPermissionsParams{}},
		{DownloadToolName, `{"url":"https://x/y","file_path":"y"}`, DownloadPermissionsParams{}},
		{FetchToolName, `{"url":"https://x"}`, FetchPermissionsParams{}},
		{WebFetchToolName, `{"url":"https://x"}`, WebFetchPermissionsParams{}},
		{WebSearchToolName, `{"query":"go"}`, WebSearchPermissionsParams{}},
		{ListMCPResourcesToolName, `{"mcp_name":"srv"}`, ListMCPResourcesPermissionsParams{}},
		{ReadMCPResourceToolName, `{"mcp_name":"srv","uri":"u"}`, ReadMCPResourcePermissionsParams{}},
	}

	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()
			preview := PreviewOf(tc.tool, tc.input, "/work")
			require.NotEmpty(t, preview.Description, "the dialog needs a description")
			require.NotNil(t, preview.Params, "the dialog type asserts on these params")
			require.IsType(t, tc.want, preview.Params)
		})
	}
}

// TestPreviewOfSkipsPlanners pins that the tools which build their own
// diff get no default preview. A preview built from their arguments
// would show the user the raw call rather than the change, so these
// tools reach the prompt through their plan instead.
func TestPreviewOfSkipsPlanners(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{
		EditToolName, MultiEditToolName, WriteToolName,
		ReplaceSymbolToolName, RenameToolName, MergeToolName,
	} {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()
			require.Nil(t, PreviewOf(tool, `{"file_path":"a.go"}`, "/work").Params)
		})
	}
}

// TestWriteToolPlans pins that write reaches the gate as a planner. The
// decorator picks the plan path by type assertion, so a tool that stops
// satisfying the interface silently loses its diff preview.
func TestWriteToolPlans(t *testing.T) {
	t.Parallel()

	require.Implements(t, (*Planner)(nil),
		NewWriteTool(nil, nil, nil, t.TempDir()))
}

// TestPreviewOfDynamicMCP pins that a dynamic MCP tool still shows the
// user the arguments it was called with, named by the identity it
// reports rather than by a guess made from its name.
func TestPreviewOfDynamicMCP(t *testing.T) {
	t.Parallel()

	tool := &Tool{mcpName: "srv", tool: &mcp.Tool{Name: "do-thing"}}
	preview := PreviewOfTool(tool, tool.Name(), `{"a":1}`, "/work")
	require.Contains(t, preview.Description, "srv/do-thing")
	require.Equal(t, `{"a":1}`, preview.Params)
}

// TestMCPIdentityComesFromTheTool pins that a dynamic MCP call is
// described by what the tool reports rather than by cutting its
// generated name at the first underscore. A server name and a tool name
// may both contain one, so the split is a guess — and the guess decides
// which rules apply, naming a server the user never wrote a rule for.
func TestMCPIdentityComesFromTheTool(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, server, mcpTool string }{
		{"plain names", "docker", "mcp-find"},
		{"underscore in the server name", "prod_admin", "delete_user"},
		{"underscore in the tool name", "docker", "compose_up"},
		{"underscores on both sides", "a_b", "c_d"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tool := &Tool{mcpName: tc.server, tool: &mcp.Tool{Name: tc.mcpTool}}

			access, ok := AccessOfTool(tool, tool.Name(), `{}`, "/work")
			require.True(t, ok)
			require.Equal(t, permission.Access{
				Tool:    tool.Name(),
				Action:  permission.ActionMCP,
				Server:  tc.server,
				MCPTool: tc.mcpTool,
			}, access)
		})
	}
}

// TestAccessOfMerge covers the one tool whose approval is not about
// reaching anything. A merge touches no file and runs no command, so
// every field but the action stays empty — and the action has to be its
// own, or the call inherits whatever the neighbouring category permits.
func TestAccessOfMerge(t *testing.T) {
	t.Parallel()

	access, ok := AccessOf(MergeToolName, `{}`, "/work")
	require.True(t, ok)
	require.Equal(t, permission.Access{
		Tool:   MergeToolName,
		Action: permission.ActionMerge,
	}, access)
}

// The proposal tools reach nothing: they revise a document held in
// memory for one branch. Mapping them anywhere stricter would put an
// approval prompt in front of every keystroke of drafting, and the gate
// that decides whether the result crosses back sits on merge.
func TestAccessOfProposalToolsOnlyRead(t *testing.T) {
	t.Parallel()

	for _, name := range []string{ProposalWriteToolName, ProposalEditToolName, ProposalReadToolName} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			access, ok := AccessOf(name, `{}`, "/work")
			require.True(t, ok)
			require.Equal(t, permission.Access{
				Tool:   name,
				Action: permission.ActionRead,
			}, access)
		})
	}
}
