package acp

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return string(data)
}

func TestPresentTool(t *testing.T) {
	t.Parallel()

	const cwd = "/work/proj"
	at := func(rel string) string { return filepath.Join(cwd, rel) }
	loc := func(rel string) []protocol.Location { return []protocol.Location{{Path: at(rel)}} }

	tests := []struct {
		name  string
		tool  string
		input string
		want  toolPresentation
	}{
		{
			name:  "read",
			tool:  toolnames.Read,
			input: mustJSON(t, tools.ReadParams{FilePath: "a.go"}),
			want:  toolPresentation{kind: protocol.ToolKindRead, title: "Read a.go", locations: loc("a.go")},
		},
		{
			name:  "read from offset starts at the next line",
			tool:  toolnames.Read,
			input: mustJSON(t, tools.ReadParams{FilePath: "a.go", Offset: 10}),
			want: toolPresentation{
				kind:      protocol.ToolKindRead,
				title:     "Read a.go",
				locations: []protocol.Location{{Path: at("a.go"), Line: new(uint32(11))}},
			},
		},
		{
			name:  "read absolute path inside cwd shows relative title",
			tool:  toolnames.Read,
			input: mustJSON(t, tools.ReadParams{FilePath: at("sub/b.go")}),
			want: toolPresentation{
				kind:      protocol.ToolKindRead,
				title:     "Read " + filepath.Join("sub", "b.go"),
				locations: loc("sub/b.go"),
			},
		},
		{
			name:  "read outside cwd shows absolute title",
			tool:  toolnames.Read,
			input: mustJSON(t, tools.ReadParams{FilePath: "/etc/hosts"}),
			want: toolPresentation{
				kind:      protocol.ToolKindRead,
				title:     "Read /etc/hosts",
				locations: []protocol.Location{{Path: "/etc/hosts"}},
			},
		},
		{
			name:  "read with empty input keeps kind",
			tool:  toolnames.Read,
			input: "",
			want:  toolPresentation{kind: protocol.ToolKindRead, title: "Read"},
		},
		{
			name:  "read with truncated input keeps kind",
			tool:  toolnames.Read,
			input: `{"file_path": "a.`,
			want:  toolPresentation{kind: protocol.ToolKindRead, title: "Read"},
		},
		{
			name:  "read mcp resource",
			tool:  toolnames.ReadMCPResource,
			input: "{}",
			want:  toolPresentation{kind: protocol.ToolKindRead, title: "ReadMCPResource"},
		},
		{
			name:  "load report",
			tool:  toolnames.LoadReport,
			input: "{}",
			want:  toolPresentation{kind: protocol.ToolKindRead, title: "LoadReport"},
		},
		{
			name:  "angela logs",
			tool:  toolnames.AngelaLogs,
			input: "{}",
			want:  toolPresentation{kind: protocol.ToolKindRead, title: "AngelaLogs"},
		},
		{
			name:  "angela info",
			tool:  toolnames.AngelaInfo,
			input: "{}",
			want:  toolPresentation{kind: protocol.ToolKindRead, title: "AngelaInfo"},
		},
		{
			name:  "edit",
			tool:  toolnames.Edit,
			input: mustJSON(t, tools.EditParams{FilePath: "a.go", OldString: "x", NewString: "y"}),
			want:  toolPresentation{kind: protocol.ToolKindEdit, title: "Edit a.go", locations: loc("a.go")},
		},
		{
			name:  "edit with empty input keeps kind",
			tool:  toolnames.Edit,
			input: "",
			want:  toolPresentation{kind: protocol.ToolKindEdit, title: "Edit"},
		},
		{
			name:  "multi edit",
			tool:  toolnames.MultiEdit,
			input: mustJSON(t, tools.MultiEditParams{FilePath: "a.go"}),
			want:  toolPresentation{kind: protocol.ToolKindEdit, title: "Edit a.go", locations: loc("a.go")},
		},
		{
			name:  "replace symbol",
			tool:  toolnames.LSPReplaceSymbol,
			input: mustJSON(t, tools.ReplaceSymbolParams{Symbol: "F", FilePath: "a.go"}),
			want:  toolPresentation{kind: protocol.ToolKindEdit, title: "Edit a.go", locations: loc("a.go")},
		},
		{
			name:  "write",
			tool:  toolnames.Write,
			input: mustJSON(t, tools.WriteParams{FilePath: "a.go", Content: "package a"}),
			want:  toolPresentation{kind: protocol.ToolKindEdit, title: "Write a.go", locations: loc("a.go")},
		},
		{
			name:  "rename",
			tool:  toolnames.LSPRename,
			input: mustJSON(t, tools.RenameParams{Symbol: "Old", NewName: "New", Path: "pkg"}),
			want: toolPresentation{
				kind:      protocol.ToolKindEdit,
				title:     "Rename Old to New",
				locations: loc("pkg"),
			},
		},
		{
			name:  "rename without path has no location",
			tool:  toolnames.LSPRename,
			input: mustJSON(t, tools.RenameParams{Symbol: "Old", NewName: "New"}),
			want:  toolPresentation{kind: protocol.ToolKindEdit, title: "Rename Old to New"},
		},
		{
			name:  "grep with path",
			tool:  toolnames.Grep,
			input: mustJSON(t, tools.GrepParams{Pattern: "TODO", Path: "pkg"}),
			want: toolPresentation{
				kind:      protocol.ToolKindSearch,
				title:     "Grep TODO in pkg",
				locations: loc("pkg"),
			},
		},
		{
			name:  "grep without path",
			tool:  toolnames.Grep,
			input: mustJSON(t, tools.GrepParams{Pattern: "TODO"}),
			want:  toolPresentation{kind: protocol.ToolKindSearch, title: "Grep TODO"},
		},
		{
			name:  "glob",
			tool:  toolnames.Glob,
			input: mustJSON(t, tools.GlobParams{Pattern: "**/*.go"}),
			want:  toolPresentation{kind: protocol.ToolKindSearch, title: "Glob **/*.go"},
		},
		{
			name:  "glob with path",
			tool:  toolnames.Glob,
			input: mustJSON(t, tools.GlobParams{Pattern: "*.go", Path: "pkg"}),
			want: toolPresentation{
				kind:      protocol.ToolKindSearch,
				title:     "Glob *.go",
				locations: loc("pkg"),
			},
		},
		{
			name:  "ls defaults to the current directory",
			tool:  toolnames.LS,
			input: "{}",
			want:  toolPresentation{kind: protocol.ToolKindSearch, title: "LS ."},
		},
		{
			name:  "ls with path",
			tool:  toolnames.LS,
			input: mustJSON(t, tools.LSParams{Path: "pkg"}),
			want:  toolPresentation{kind: protocol.ToolKindSearch, title: "LS pkg", locations: loc("pkg")},
		},
		{
			name:  "ls with empty input",
			tool:  toolnames.LS,
			input: "",
			want:  toolPresentation{kind: protocol.ToolKindSearch, title: "LS"},
		},
		{
			name:  "sourcegraph",
			tool:  toolnames.Sourcegraph,
			input: mustJSON(t, tools.SourcegraphParams{Query: "lang:go foo"}),
			want:  toolPresentation{kind: protocol.ToolKindSearch, title: "Sourcegraph lang:go foo"},
		},
		{
			name:  "references",
			tool:  toolnames.LSPReferences,
			input: mustJSON(t, tools.ReferencesParams{Symbol: "Foo", Path: "pkg"}),
			want: toolPresentation{
				kind:      protocol.ToolKindSearch,
				title:     "LSPReferences Foo",
				locations: loc("pkg"),
			},
		},
		{
			name:  "definition",
			tool:  toolnames.LSPDefinition,
			input: mustJSON(t, tools.DefinitionParams{Symbol: "Foo"}),
			want:  toolPresentation{kind: protocol.ToolKindSearch, title: "LSPDefinition Foo"},
		},
		{
			name:  "call hierarchy",
			tool:  toolnames.LSPCallHierarchy,
			input: mustJSON(t, tools.CallHierarchyParams{Symbol: "Foo", Direction: "incoming"}),
			want:  toolPresentation{kind: protocol.ToolKindSearch, title: "LSPCallHierarchy Foo"},
		},
		{
			name:  "diagnostics for a file",
			tool:  toolnames.LSPDiagnostics,
			input: mustJSON(t, tools.DiagnosticsParams{FilePath: "a.go"}),
			want: toolPresentation{
				kind:      protocol.ToolKindSearch,
				title:     "LSPDiagnostics a.go",
				locations: loc("a.go"),
			},
		},
		{
			name:  "diagnostics for the project",
			tool:  toolnames.LSPDiagnostics,
			input: "{}",
			want:  toolPresentation{kind: protocol.ToolKindSearch, title: "LSPDiagnostics"},
		},
		{
			name:  "symbols",
			tool:  toolnames.LSPSymbols,
			input: mustJSON(t, tools.SymbolsParams{FilePath: "a.go"}),
			want: toolPresentation{
				kind:      protocol.ToolKindSearch,
				title:     "LSPSymbols a.go",
				locations: loc("a.go"),
			},
		},
		{
			name:  "list mcp resources",
			tool:  toolnames.ListMCPResources,
			input: "{}",
			want:  toolPresentation{kind: protocol.ToolKindSearch, title: "ListMCPResources"},
		},
		{
			name:  "bash",
			tool:  toolnames.Bash,
			input: mustJSON(t, tools.BashParams{Command: "go test ./...", Description: "run tests"}),
			want:  toolPresentation{kind: protocol.ToolKindExecute, title: "go test ./..."},
		},
		{
			name:  "bash shows only the first line",
			tool:  toolnames.Bash,
			input: mustJSON(t, tools.BashParams{Command: "set -e\ngo test ./...\n"}),
			want:  toolPresentation{kind: protocol.ToolKindExecute, title: "set -e…"},
		},
		{
			name:  "bash without command",
			tool:  toolnames.Bash,
			input: "{}",
			want:  toolPresentation{kind: protocol.ToolKindExecute, title: "Bash"},
		},
		{
			name:  "git",
			tool:  toolnames.Git,
			input: mustJSON(t, tools.GitParams{Args: []string{"log", "--oneline"}}),
			want:  toolPresentation{kind: protocol.ToolKindExecute, title: "git log --oneline"},
		},
		{
			name:  "job output",
			tool:  toolnames.JobOutput,
			input: mustJSON(t, tools.JobOutputParams{ShellID: "001"}),
			want:  toolPresentation{kind: protocol.ToolKindExecute, title: "JobOutput 001"},
		},
		{
			name:  "job kill",
			tool:  toolnames.JobKill,
			input: mustJSON(t, tools.JobKillParams{ShellID: "001"}),
			want:  toolPresentation{kind: protocol.ToolKindExecute, title: "JobKill 001"},
		},
		{
			name:  "fetch",
			tool:  toolnames.Fetch,
			input: mustJSON(t, tools.FetchParams{URL: "https://example.com", Format: "text"}),
			want:  toolPresentation{kind: protocol.ToolKindFetch, title: "https://example.com"},
		},
		{
			name:  "web fetch",
			tool:  toolnames.WebFetch,
			input: mustJSON(t, tools.WebFetchParams{URL: "https://example.com"}),
			want:  toolPresentation{kind: protocol.ToolKindFetch, title: "https://example.com"},
		},
		{
			name:  "download",
			tool:  toolnames.Download,
			input: mustJSON(t, tools.DownloadParams{URL: "https://example.com/x.zip", FilePath: "x.zip"}),
			want: toolPresentation{
				kind:      protocol.ToolKindFetch,
				title:     "https://example.com/x.zip",
				locations: loc("x.zip"),
			},
		},
		{
			name:  "web search",
			tool:  toolnames.WebSearch,
			input: mustJSON(t, tools.WebSearchParams{Query: "go 1.26"}),
			want:  toolPresentation{kind: protocol.ToolKindFetch, title: "go 1.26"},
		},
		{
			name:  "multi search",
			tool:  toolnames.MultiSearch,
			input: mustJSON(t, tools.MultiSearchParams{Query: "go 1.26"}),
			want:  toolPresentation{kind: protocol.ToolKindFetch, title: "go 1.26"},
		},
		{
			name:  "todos",
			tool:  toolnames.Todos,
			input: "{}",
			want:  toolPresentation{kind: protocol.ToolKindThink, title: "Update todos"},
		},
		{
			name:  "agent uses its description",
			tool:  toolnames.Agent,
			input: mustJSON(t, agent.AgentParams{Description: "Find callers", Prompt: "find them"}),
			want:  toolPresentation{kind: protocol.ToolKindOther, title: "Find callers"},
		},
		{
			name:  "agent without description",
			tool:  toolnames.Agent,
			input: mustJSON(t, agent.AgentParams{Prompt: "find them"}),
			want:  toolPresentation{kind: protocol.ToolKindOther, title: "Agent"},
		},
		{
			name:  "mcp tool",
			tool:  "MCP_github_search",
			input: `{"q": "x"}`,
			want:  toolPresentation{kind: protocol.ToolKindOther, title: "MCP_github_search"},
		},
		{
			name:  "question",
			tool:  toolnames.Question,
			input: "{}",
			want:  toolPresentation{kind: protocol.ToolKindOther, title: "Question"},
		},
		{
			name:  "unknown tool",
			tool:  "Whatever",
			input: "{}",
			want:  toolPresentation{kind: protocol.ToolKindOther, title: "Whatever"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			call := message.ToolCall{ID: "c1", Name: tt.tool, Input: tt.input}
			require.Equal(t, tt.want, presentTool(call, cwd))
		})
	}
}

func TestToolResultContent(t *testing.T) {
	t.Parallel()

	const cwd = "/work/proj"
	at := func(rel string) string { return filepath.Join(cwd, rel) }
	text := func(s string) []protocol.ToolCallContent {
		return []protocol.ToolCallContent{protocol.ToolContent{Block: protocol.TextBlock{Text: s}}}
	}
	diff := func(path string, oldText *string, newText string) protocol.ToolDiff {
		return protocol.ToolDiff{Path: path, OldText: oldText, NewText: newText}
	}

	editCall := message.ToolCall{
		ID: "c1", Name: toolnames.Edit, Finished: true,
		Input: mustJSON(t, tools.EditParams{FilePath: "a.go", OldString: "a", NewString: "b"}),
	}
	multiEditCall := message.ToolCall{
		ID: "c1", Name: toolnames.MultiEdit, Finished: true,
		Input: mustJSON(t, tools.MultiEditParams{FilePath: "/abs/a.go"}),
	}
	writeCall := message.ToolCall{
		ID: "c1", Name: toolnames.Write, Finished: true,
		Input: mustJSON(t, tools.WriteParams{FilePath: "a.go", Content: "new body"}),
	}
	editResult := func(meta tools.EditResponseMetadata) message.ToolResult {
		return message.ToolResult{
			ToolCallID: "c1", Name: toolnames.Edit, Content: "edited",
			Metadata: mustJSON(t, meta),
		}
	}
	image := []byte{0x89, 'P', 'N', 'G'}

	tests := []struct {
		name   string
		result message.ToolResult
		call   message.ToolCall
		found  bool
		want   []protocol.ToolCallContent
	}{
		{
			name:   "edit becomes a diff against the old content",
			result: editResult(tools.EditResponseMetadata{OldContent: "a", NewContent: "b"}),
			call:   editCall,
			found:  true,
			want:   []protocol.ToolCallContent{diff(at("a.go"), new("a"), "b")},
		},
		{
			name:   "edit of an empty existing file keeps an empty old text",
			result: editResult(tools.EditResponseMetadata{OldContent: "", NewContent: "b"}),
			call:   editCall,
			found:  true,
			want:   []protocol.ToolCallContent{diff(at("a.go"), new(""), "b")},
		},
		{
			name:   "edit that creates a file has no old text",
			result: editResult(tools.EditResponseMetadata{NewContent: "b", Created: true}),
			call:   editCall,
			found:  true,
			want:   []protocol.ToolCallContent{diff(at("a.go"), nil, "b")},
		},
		{
			name: "multi edit keeps an absolute path",
			result: message.ToolResult{
				ToolCallID: "c1", Name: toolnames.MultiEdit, Content: "edited",
				Metadata: mustJSON(t, tools.MultiEditResponseMetadata{OldContent: "a", NewContent: "b"}),
			},
			call:  multiEditCall,
			found: true,
			want:  []protocol.ToolCallContent{diff("/abs/a.go", new("a"), "b")},
		},
		{
			name: "write takes the new text from the call input",
			result: message.ToolResult{
				ToolCallID: "c1", Name: toolnames.Write, Content: "written",
				Metadata: mustJSON(t, tools.WriteResponseMetadata{OldContent: "old"}),
			},
			call:  writeCall,
			found: true,
			want:  []protocol.ToolCallContent{diff(at("a.go"), new("old"), "new body")},
		},
		{
			name: "write that creates a file has no old text",
			result: message.ToolResult{
				ToolCallID: "c1", Name: toolnames.Write, Content: "written",
				Metadata: mustJSON(t, tools.WriteResponseMetadata{Created: true}),
			},
			call:  writeCall,
			found: true,
			want:  []protocol.ToolCallContent{diff(at("a.go"), nil, "new body")},
		},
		{
			name: "replace symbol needs no call",
			result: message.ToolResult{
				ToolCallID: "c1", Name: toolnames.LSPReplaceSymbol, Content: "replaced",
				Metadata: mustJSON(t, tools.ReplaceSymbolResponseMetadata{
					FilePath: "a.go", OldContent: "old", NewContent: "new",
				}),
			},
			want: []protocol.ToolCallContent{diff(at("a.go"), new("old"), "new")},
		},
		{
			name: "rename yields one diff per changed file",
			result: message.ToolResult{
				ToolCallID: "c1", Name: toolnames.LSPRename, Content: "renamed",
				Metadata: mustJSON(t, tools.RenameResponseMetadata{Files: []tools.RenamedFile{
					{Path: "a.go", OldContent: "Old", NewContent: "New"},
					{Path: "", OldContent: "skipped", NewContent: "skipped"},
					{Path: "/abs/b.go", OldContent: "Old2", NewContent: "New2"},
				}}),
			},
			want: []protocol.ToolCallContent{
				diff(at("a.go"), new("Old"), "New"),
				diff("/abs/b.go", new("Old2"), "New2"),
			},
		},
		{
			name:   "edit without its call falls back to text",
			result: editResult(tools.EditResponseMetadata{OldContent: "a", NewContent: "b"}),
			want:   text("edited"),
		},
		{
			name:   "edit with an undecodable call falls back to text",
			result: editResult(tools.EditResponseMetadata{OldContent: "a", NewContent: "b"}),
			call:   message.ToolCall{ID: "c1", Name: toolnames.Edit, Input: `{"file_path":`},
			found:  true,
			want:   text("edited"),
		},
		{
			name: "edit with broken metadata falls back to text",
			result: message.ToolResult{
				ToolCallID: "c1", Name: toolnames.Edit, Content: "edited", Metadata: "not json",
			},
			call:  editCall,
			found: true,
			want:  text("edited"),
		},
		{
			name: "edit with no metadata falls back to text",
			result: message.ToolResult{
				ToolCallID: "c1", Name: toolnames.Edit, Content: "edited",
			},
			call:  editCall,
			found: true,
			want:  text("edited"),
		},
		{
			name: "rename without changed files falls back to text",
			result: message.ToolResult{
				ToolCallID: "c1", Name: toolnames.LSPRename, Content: "nothing to rename",
				Metadata: mustJSON(t, tools.RenameResponseMetadata{}),
			},
			want: text("nothing to rename"),
		},
		{
			name: "failed edit shows the error and no diff",
			result: message.ToolResult{
				ToolCallID: "c1", Name: toolnames.Edit, Content: "user denied", IsError: true,
				Metadata: mustJSON(t, tools.EditResponseMetadata{OldContent: "a", NewContent: "b"}),
			},
			call:  editCall,
			found: true,
			want:  text("user denied"),
		},
		{
			name: "image result",
			result: message.ToolResult{
				ToolCallID: "c1", Name: toolnames.Read, Content: "Loaded image/png content",
				Data: base64.StdEncoding.EncodeToString(image), MIMEType: "image/png",
			},
			want: []protocol.ToolCallContent{
				protocol.ToolContent{Block: protocol.ImageBlock{MIMEType: "image/png", Data: image}},
			},
		},
		{
			name: "image with broken base64 falls back to text",
			result: message.ToolResult{
				ToolCallID: "c1", Name: toolnames.Read, Content: "Loaded image/png content",
				Data: "!!!", MIMEType: "image/png",
			},
			want: text("Loaded image/png content"),
		},
		{
			name: "image without a mime type falls back to text",
			result: message.ToolResult{
				ToolCallID: "c1", Name: toolnames.Read, Content: "caption",
				Data: base64.StdEncoding.EncodeToString(image),
			},
			want: text("caption"),
		},
		{
			name:   "failed image result shows the error",
			result: message.ToolResult{ToolCallID: "c1", Name: toolnames.Read, Content: "bad encoding", IsError: true, Data: "AAAA", MIMEType: "image/png"},
			want:   text("bad encoding"),
		},
		{
			name:   "plain text",
			result: message.ToolResult{ToolCallID: "c1", Name: toolnames.Bash, Content: "ok\n"},
			want:   text("ok\n"),
		},
		{
			name:   "empty result has no content",
			result: message.ToolResult{ToolCallID: "c1", Name: toolnames.Bash},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, toolResultContent(tt.result, tt.call, tt.found, cwd))
		})
	}
}
