package chat

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/hooks"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// newTestBaseTool builds a bare baseToolMessageItem for exercising the
// clipboard-copy formatting helpers directly, without needing a real
// ToolRenderer (those methods never touch it).
func newTestBaseTool(sty *styles.Styles, toolCall message.ToolCall, result *message.ToolResult) *baseToolMessageItem {
	return newBaseToolMessageItem(sty, toolCall, result, nil, false)
}

// fileLangExtensionCases enumerates the file-extension-to-language mapping
// shared verbatim between formatViewResultForCopy and formatWriteResultForCopy.
var fileLangExtensionCases = []struct {
	path string
	lang string
}{
	{"a.go", "go"},
	{"a.js", "javascript"},
	{"a.mjs", "javascript"},
	{"a.ts", "typescript"},
	{"a.py", "python"},
	{"a.rs", "rust"},
	{"a.java", "java"},
	{"a.c", "c"},
	{"a.cpp", "cpp"},
	{"a.cc", "cpp"},
	{"a.cxx", "cpp"},
	{"a.sh", "bash"},
	{"a.bash", "bash"},
	{"a.json", "json"},
	{"a.yaml", "yaml"},
	{"a.yml", "yaml"},
	{"a.xml", "xml"},
	{"a.html", "html"},
	{"a.css", "css"},
	{"a.md", "markdown"},
	{"a.unknownext", ""},
}

func TestDefaultToolRenderContext_RenderTool(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	d := &DefaultToolRenderContext{}
	out := d.RenderTool(&sty, 80, &ToolRenderOpts{ToolCall: message.ToolCall{Name: "mystery_tool"}})
	require.Equal(t, "TODO: Implement Tool Renderer For: mystery_tool", out)
}

// NewToolMessageItem is the single dispatch point routing every known tool
// name to its dedicated renderer; every branch must produce a usable item
// with the message ID attached.
func TestNewToolMessageItem_DispatchesByName(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	tests := []struct {
		name     string
		toolName string
	}{
		{"bash", toolnames.Bash},
		{"job_output", toolnames.JobOutput},
		{"job_kill", toolnames.JobKill},
		{"view", toolnames.View},
		{"write", toolnames.Write},
		{"edit", toolnames.Edit},
		{"multiedit", toolnames.MultiEdit},
		{"glob", toolnames.Glob},
		{"grep", toolnames.Grep},
		{"ls", toolnames.LS},
		{"download", toolnames.Download},
		{"fetch", toolnames.Fetch},
		{"sourcegraph", toolnames.Sourcegraph},
		{"lsp_diagnostics", toolnames.LSPDiagnostics},
		{"agent", toolnames.Agent},
		{"webfetch", toolnames.WebFetch},
		{"websearch", toolnames.WebSearch},
		{"todos", toolnames.Todos},
		{"question", toolnames.Question},
		{"lsp_references", toolnames.LSPReferences},
		{"lsp_definition", toolnames.LSPDefinition},
		{"lsp_rename", toolnames.LSPRename},
		{"lsp_replace_symbol", toolnames.LSPReplaceSymbol},
		{"lsp_call_hierarchy", toolnames.LSPCallHierarchy},
		{"lsp_symbols", toolnames.LSPSymbols},
		{"lsp_restart", toolnames.LSPRestart},
		{"docker_mcp", toolnames.MCPPrefix + config.DockerMCPName + "_ps"},
		{"generic_mcp", toolnames.MCPPrefix + "custom_thing"},
		{"unknown", "totally_unknown_tool"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			toolCall := message.ToolCall{ID: "id-" + tt.name, Name: tt.toolName, Input: "{}", Finished: true}
			item := NewToolMessageItem(&sty, "msg-42", toolCall, nil, false, "/tmp")
			require.NotNil(t, item)
			require.Equal(t, "msg-42", item.MessageID())
			require.Equal(t, tt.toolName, item.ToolCall().Name)
		})
	}
}

func TestBaseToolMessageItem_StartAnimation(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "sym-1", Name: toolnames.LSPSymbols, Input: "{}", Finished: false}
	item := NewSymbolsToolMessageItem(&sty, toolCall, nil, false)
	base := item.(*baseToolMessageItem)

	require.NotNil(t, base.StartAnimation(), "a pending tool call should start its spinner")

	base.SetStatus(ToolStatusCanceled)
	require.Nil(t, base.StartAnimation(), "a canceled tool call must not spin")
}

func TestBaseToolMessageItem_HandleMouseClick(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ref-1", Name: toolnames.LSPReferences, Input: "{}", Finished: true}
	item := NewReferencesToolMessageItem(&sty, toolCall, nil, false)
	base := item.(*baseToolMessageItem)

	require.True(t, base.HandleMouseClick(ansi.MouseLeft, 0, 0))
	require.False(t, base.HandleMouseClick(ansi.MouseRight, 0, 0))
}

func TestBaseToolMessageItem_HandleKeyEvent(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "ref-2", Name: toolnames.LSPReferences, Input: `{"symbol":"Foo"}`, Finished: true}
	result := &message.ToolResult{ToolCallID: "ref-2", Content: "found 3 references"}
	item := NewReferencesToolMessageItem(&sty, toolCall, result, false)
	base := item.(*baseToolMessageItem)

	for _, k := range []string{"c", "y"} {
		handled, cmd := base.HandleKeyEvent(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
		require.True(t, handled)
		require.NotNil(t, cmd)
	}

	handled, cmd := base.HandleKeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.False(t, handled)
	require.Nil(t, cmd)
}

func TestBaseToolMessageItem_SetSpinningFunc(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "sym-3", Name: toolnames.LSPSymbols, Input: "{}", Finished: true}
	item := NewSymbolsToolMessageItem(&sty, toolCall, nil, false)
	base := item.(*baseToolMessageItem)

	require.False(t, base.isSpinning(), "a finished tool call should not spin by default")

	base.SetSpinningFunc(func(state SpinningState) bool {
		return true
	})
	require.True(t, base.isSpinning(), "a custom spinning func should override the default logic")
}

func TestToolOutputSkillContent(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	out := ansi.Strip(toolOutputSkillContent(&sty, "jq-helper", "Query JSON with jq"))
	require.Contains(t, out, "Loaded Skill")
	require.Contains(t, out, "jq-helper")
	require.Contains(t, out, "Query JSON with jq")
}

func TestToolOutputHookIndicator(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	require.Equal(t, "", toolOutputHookIndicator(&sty, "", 80))
	require.Equal(t, "", toolOutputHookIndicator(&sty, "not json", 80))
	require.Equal(t, "", toolOutputHookIndicator(&sty, `{"hook":null}`, 80))
	require.Equal(t, "", toolOutputHookIndicator(&sty, `{"hook":{"hooks":[]}}`, 80))

	meta := `{"hook":{"hooks":[` +
		`{"name":"format.sh","matcher":"*.go","decision":"allow"},` +
		`{"name":"guard.sh","decision":"deny","reason":"blocked by policy"},` +
		`{"name":"rewrite.sh","decision":"allow","input_rewrite":true}` +
		`]}}`
	out := ansi.Strip(toolOutputHookIndicator(&sty, meta, 120))
	lines := strings.Split(out, "\n")
	require.Len(t, lines, 3, "one line per hook")
	require.Contains(t, out, "format.sh")
	require.Contains(t, out, "*.go")
	require.Contains(t, out, "OK")
	require.Contains(t, out, "guard.sh")
	require.Contains(t, out, "Denied")
	require.Contains(t, out, "blocked by policy")
	require.Contains(t, out, "rewrite.sh")
	require.Contains(t, out, "Rewrote Output")

	// The name column is capped at 30 cells, so an overlong path-like
	// name is left-truncated to keep its most useful (trailing) part.
	longPathMeta := `{"hook":{"hooks":[{"name":"/very/long/absolute/path/to/some/hook/script/format_long_name.sh","decision":"allow"}]}}`
	pathOut := ansi.Strip(toolOutputHookIndicator(&sty, longPathMeta, 120))
	require.Contains(t, pathOut, "…")
	require.Contains(t, pathOut, "format_long_name.sh")
	require.NotContains(t, pathOut, "/very/long/absolute")

	// A long name with no path shape is right-truncated instead.
	longWordsMeta := `{"hook":{"hooks":[{"name":"this hook name is definitely way too long for the column width","decision":"allow"}]}}`
	wordsOut := ansi.Strip(toolOutputHookIndicator(&sty, longWordsMeta, 120))
	require.Contains(t, wordsOut, "…")
	require.Contains(t, wordsOut, "this hook name is")
	require.NotContains(t, wordsOut, "column width")
}

func TestTruncateHookName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "short.sh", truncateHookName("short.sh", 30), "names within budget are unchanged")

	longPath := "/a/b/c/d/e/f/g/h/i/j/format.sh"
	gotPath := truncateHookName(longPath, 10)
	require.True(t, strings.HasPrefix(gotPath, "…"), "path-like names are left-truncated")
	require.True(t, strings.HasSuffix(gotPath, "format.sh"))

	longWords := "this is a very long non path hook name"
	gotWords := truncateHookName(longWords, 10)
	require.True(t, strings.HasSuffix(gotWords, "…"), "non-path names are right-truncated")
	require.True(t, strings.HasPrefix(gotWords, "this is a"))
}

func TestIsLikelyPath(t *testing.T) {
	t.Parallel()

	require.True(t, isLikelyPath("/abs/path/script.sh"))
	require.True(t, isLikelyPath("rel/path/script.sh"))
	require.False(t, isLikelyPath("no-slash-name"))
	require.False(t, isLikelyPath("has space/path.sh"))
	require.False(t, isLikelyPath(""))
	require.False(t, isLikelyPath("weird¶name/path"))
}

func TestHookDetail(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	deny := ansi.Strip(hookDetail(&sty, hooks.HookInfo{Decision: "deny", Reason: "nope"}))
	require.Contains(t, deny, "Denied")
	require.Contains(t, deny, "nope")

	denyNoReason := ansi.Strip(hookDetail(&sty, hooks.HookInfo{Decision: "deny"}))
	require.Equal(t, "Denied", denyNoReason)

	allow := ansi.Strip(hookDetail(&sty, hooks.HookInfo{Decision: "allow"}))
	require.Equal(t, "OK", allow)

	allowRewrite := ansi.Strip(hookDetail(&sty, hooks.HookInfo{Decision: "allow", InputRewrite: true}))
	require.Contains(t, allowRewrite, "OK")
	require.Contains(t, allowRewrite, "Rewrote Output")

	def := ansi.Strip(hookDetail(&sty, hooks.HookInfo{Decision: "", InputRewrite: true}))
	require.Contains(t, def, "OK")
	require.Contains(t, def, "Rewrote Output")
}

func TestRenderHookLine(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	withMatcher := ansi.Strip(renderHookLine(&sty, hooks.HookInfo{Name: "fmt.sh", Matcher: "*.go", Decision: "allow"}, "fmt.sh", "OK", 10, 4))
	require.Contains(t, withMatcher, "Hook")
	require.Contains(t, withMatcher, "fmt.sh")
	require.Contains(t, withMatcher, "*.go")
	require.Contains(t, withMatcher, "OK")

	noMatcherColumn := ansi.Strip(renderHookLine(&sty, hooks.HookInfo{Name: "guard.sh", Decision: "deny"}, "guard.sh", "Denied", 10, 0))
	require.Contains(t, noMatcherColumn, "guard.sh")
	require.Contains(t, noMatcherColumn, "Denied")
}

func TestFormatSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		bytes int
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1536, "1.5 KB"},
		{5 * 1024 * 1024, "5.0 MB"},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, formatSize(tt.bytes))
	}
}

func TestFormatTimeout(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", formatTimeout(0))
	require.Equal(t, "30s", formatTimeout(30))
}

func TestFormatNonZero(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", formatNonZero(0))
	require.Equal(t, "7", formatNonZero(7))
}

func TestToolOutputMultiEditDiffContent(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	meta := tools.MultiEditResponseMetadata{
		OldContent: "line1\nline2\n",
		NewContent: "line1\nchanged\n",
	}
	out := ansi.Strip(toolOutputMultiEditDiffContent(&sty, "a.go", meta, 1, 100, false))
	require.Contains(t, out, "changed")

	metaFailed := tools.MultiEditResponseMetadata{
		OldContent:   "line1\n",
		NewContent:   "line1x\n",
		EditsApplied: 1,
		EditsFailed: []tools.FailedEdit{
			{Index: 1, Error: "no match", Edit: tools.MultiEditOperation{OldString: "zz"}},
		},
	}
	withNote := ansi.Strip(toolOutputMultiEditDiffContent(&sty, "a.go", metaFailed, 2, 100, false))
	require.Contains(t, withNote, "Note")
	require.Contains(t, withNote, "1 of 2 edits succeeded")

	wide := ansi.Strip(toolOutputMultiEditDiffContent(&sty, "a.go", meta, 1, 200, false))
	require.Contains(t, wide, "changed", "a wide terminal still renders the diff, just split")

	var oldB, newB strings.Builder
	for i := range 30 {
		fmt.Fprintf(&oldB, "old %d\n", i)
		fmt.Fprintf(&newB, "new %d\n", i)
	}
	bigMeta := tools.MultiEditResponseMetadata{OldContent: oldB.String(), NewContent: newB.String()}
	truncated := ansi.Strip(toolOutputMultiEditDiffContent(&sty, "a.go", bigMeta, 1, 100, false))
	require.Contains(t, truncated, "hidden")
	expanded := ansi.Strip(toolOutputMultiEditDiffContent(&sty, "a.go", bigMeta, 1, 100, true))
	require.NotContains(t, expanded, "hidden")
}

func TestFormatParametersForCopy(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	tests := []struct {
		name     string
		toolName string
		input    string
		contains []string
	}{
		{"bash", toolnames.Bash, `{"command":"echo hi\nthere","description":"say hi"}`, []string{"**Command:** echo hi", "there"}},
		{"view", toolnames.View, `{"file_path":"/tmp/a.go","limit":10,"offset":5}`, []string{"**File:**", "a.go", "**Limit:** 10", "**Offset:** 5"}},
		{"edit", toolnames.Edit, `{"file_path":"/tmp/a.go"}`, []string{"**File:**", "a.go"}},
		{"multiedit", toolnames.MultiEdit, `{"file_path":"/tmp/a.go","edits":[{"old_string":"a","new_string":"b"}]}`, []string{"**File:**", "**Edits:** 1"}},
		{"write", toolnames.Write, `{"file_path":"/tmp/a.go","content":"x"}`, []string{"**File:**", "a.go"}},
		{"fetch", toolnames.Fetch, `{"url":"http://x","format":"text","timeout":5}`, []string{"**URL:** http://x", "**Format:** text", "**Timeout:** 5s"}},
		{"webfetch", toolnames.WebFetch, `{"url":"http://x"}`, []string{"**URL:** http://x"}},
		{"grep", toolnames.Grep, `{"pattern":"foo","path":"/tmp","include":"*.go","literal_text":true}`, []string{"**Pattern:** foo", "**Path:** /tmp", "**Include:** *.go", "**Literal:** true"}},
		{"glob", toolnames.Glob, `{"pattern":"*.go","path":"/tmp"}`, []string{"**Pattern:** *.go", "**Path:** /tmp"}},
		{"ls_with_path", toolnames.LS, `{"path":"/tmp"}`, []string{"**Path:**", "tmp"}},
		{"ls_default_path", toolnames.LS, `{}`, []string{"**Path:**"}},
		{"download", toolnames.Download, `{"url":"http://x","file_path":"/tmp/a","timeout":5}`, []string{"**URL:** http://x", "**File Path:**", "**Timeout:** 5s"}},
		{"sourcegraph", toolnames.Sourcegraph, `{"query":"foo","count":5,"context_window":10}`, []string{"**Query:** foo", "**Count:** 5", "**Context:** 10"}},
		{"lsp_diagnostics", toolnames.LSPDiagnostics, `{}`, []string{"**Project:** diagnostics"}},
		{"agent", toolnames.Agent, `{"prompt":"do the thing"}`, []string{"**Task:**", "do the thing"}},
		{"unknown_tool_generic_map", "totally_custom_tool", `{"foo_bar":"baz"}`, []string{"**Foo bar:** baz"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			toolCall := message.ToolCall{ID: "id", Name: tt.toolName, Input: tt.input}
			item := newTestBaseTool(&sty, toolCall, nil)
			out := item.formatParametersForCopy()
			for _, c := range tt.contains {
				require.Contains(t, out, c)
			}
		})
	}

	t.Run("invalid_json_returns_empty", func(t *testing.T) {
		t.Parallel()
		toolCall := message.ToolCall{ID: "id", Name: toolnames.Bash, Input: "not json"}
		item := newTestBaseTool(&sty, toolCall, nil)
		require.Equal(t, "", item.formatParametersForCopy())
	})
}

func TestFormatResultForCopy(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	t.Run("nil_result", func(t *testing.T) {
		t.Parallel()
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Bash}, nil)
		require.Equal(t, "", item.formatResultForCopy())
	})

	t.Run("image_data", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Data: "abc", MIMEType: "image/png"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.View}, result)
		require.Equal(t, "[Image: image/png]", item.formatResultForCopy())
	})

	t.Run("other_media_data", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Data: "abc", MIMEType: "audio/mpeg"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.View}, result)
		require.Equal(t, "[Media: audio/mpeg]", item.formatResultForCopy())
	})

	t.Run("code_fenced_tools", func(t *testing.T) {
		t.Parallel()
		for _, name := range []string{
			toolnames.Download, toolnames.Grep, toolnames.Glob, toolnames.LS,
			toolnames.Sourcegraph, toolnames.LSPDiagnostics, toolnames.Todos,
		} {
			result := &message.ToolResult{ToolCallID: "x", Content: "some output"}
			item := newTestBaseTool(&sty, message.ToolCall{Name: name}, result)
			require.Equal(t, "```\nsome output\n```", item.formatResultForCopy())
		}
	})

	t.Run("default_raw_content", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "raw"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.WebSearch}, result)
		require.Equal(t, "raw", item.formatResultForCopy())
	})

	// Each of these dispatches to its own formatXResultForCopy helper;
	// route through the switch itself so that dispatch is covered too,
	// not just the helpers in isolation.
	t.Run("dispatches_bash", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "bash out"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Bash}, result)
		require.Equal(t, "```bash\nbash out\n```", item.formatResultForCopy())
	})

	t.Run("dispatches_view", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "view out"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.View}, result)
		require.Equal(t, "view out", item.formatResultForCopy())
	})

	t.Run("dispatches_edit", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "edit out"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Edit}, result)
		require.Equal(t, "edit out", item.formatResultForCopy())
	})

	t.Run("dispatches_multiedit", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "multiedit out"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.MultiEdit}, result)
		require.Equal(t, "multiedit out", item.formatResultForCopy())
	})

	t.Run("dispatches_write", func(t *testing.T) {
		t.Parallel()
		input, err := json.Marshal(tools.WriteParams{FilePath: "a.go", Content: "x"})
		require.NoError(t, err)
		toolCall := message.ToolCall{Name: toolnames.Write, Input: string(input)}
		result := &message.ToolResult{ToolCallID: "x"}
		item := newTestBaseTool(&sty, toolCall, result)
		require.Contains(t, item.formatResultForCopy(), "```go")
	})

	t.Run("dispatches_fetch", func(t *testing.T) {
		t.Parallel()
		toolCall := message.ToolCall{Name: toolnames.Fetch, Input: `{"url":"http://x"}`}
		result := &message.ToolResult{ToolCallID: "x", Content: "fetched"}
		item := newTestBaseTool(&sty, toolCall, result)
		require.Contains(t, item.formatResultForCopy(), "fetched")
	})

	t.Run("dispatches_webfetch", func(t *testing.T) {
		t.Parallel()
		toolCall := message.ToolCall{Name: toolnames.WebFetch, Input: `{"url":"http://x"}`}
		result := &message.ToolResult{ToolCallID: "x", Content: "fetched md"}
		item := newTestBaseTool(&sty, toolCall, result)
		require.Contains(t, item.formatResultForCopy(), "fetched md")
	})

	t.Run("dispatches_agent", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "agent summary"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Agent}, result)
		require.Contains(t, item.formatResultForCopy(), "agent summary")
	})
}

// ToolRendererFunc lets a plain function satisfy ToolRenderer without a
// dedicated named type.
func TestToolRendererFunc_RenderTool(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	var f ToolRendererFunc = func(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
		return fmt.Sprintf("rendered:%s:%d", opts.ToolCall.Name, width)
	}
	out := f.RenderTool(&sty, 42, &ToolRenderOpts{ToolCall: message.ToolCall{Name: "x"}})
	require.Equal(t, "rendered:x:42", out)
}

func TestFormatBashResultForCopy(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	t.Run("nil_result", func(t *testing.T) {
		t.Parallel()
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Bash}, nil)
		require.Equal(t, "", item.formatBashResultForCopy())
	})

	t.Run("metadata_output", func(t *testing.T) {
		t.Parallel()
		meta := tools.BashResponseMetadata{Output: "hello world"}
		metaJSON, err := json.Marshal(meta)
		require.NoError(t, err)
		result := &message.ToolResult{ToolCallID: "x", Metadata: string(metaJSON), Content: tools.BashNoOutput}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Bash}, result)
		require.Equal(t, "```bash\nhello world\n```", item.formatBashResultForCopy())
	})

	t.Run("falls_back_to_content", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "raw output"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Bash}, result)
		require.Equal(t, "```bash\nraw output\n```", item.formatBashResultForCopy())
	})

	t.Run("no_output_at_all", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: tools.BashNoOutput}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Bash}, result)
		require.Equal(t, "", item.formatBashResultForCopy())
	})
}

func TestFormatViewResultForCopy(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	t.Run("nil_result", func(t *testing.T) {
		t.Parallel()
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.View}, nil)
		require.Equal(t, "", item.formatViewResultForCopy())
	})

	t.Run("no_metadata_content_falls_back", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "raw text"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.View}, result)
		require.Equal(t, "raw text", item.formatViewResultForCopy())
	})

	for _, tt := range fileLangExtensionCases {
		t.Run("lang_"+tt.path, func(t *testing.T) {
			t.Parallel()
			meta := tools.ViewResponseMetadata{FilePath: tt.path, Content: "body text"}
			metaJSON, err := json.Marshal(meta)
			require.NoError(t, err)
			result := &message.ToolResult{ToolCallID: "x", Metadata: string(metaJSON)}
			item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.View}, result)
			out := item.formatViewResultForCopy()
			if tt.lang != "" {
				require.Contains(t, out, "```"+tt.lang)
			} else {
				require.True(t, strings.HasPrefix(out, "```\n"))
			}
			require.Contains(t, out, "body text")
		})
	}
}

func TestFormatEditResultForCopy(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	t.Run("nil_result", func(t *testing.T) {
		t.Parallel()
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Edit}, nil)
		require.Equal(t, "", item.formatEditResultForCopy())
	})

	t.Run("no_metadata_falls_back_to_content", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "raw"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Edit}, result)
		require.Equal(t, "raw", item.formatEditResultForCopy())
	})

	t.Run("invalid_metadata_falls_back_to_content", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "raw", Metadata: "not json"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Edit}, result)
		require.Equal(t, "raw", item.formatEditResultForCopy())
	})

	t.Run("valid_metadata_with_content_shows_diff", func(t *testing.T) {
		t.Parallel()
		meta := tools.EditResponseMetadata{OldContent: "a\n", NewContent: "b\n"}
		metaJSON, err := json.Marshal(meta)
		require.NoError(t, err)
		toolCall := message.ToolCall{Name: toolnames.Edit, Input: `{"file_path":"/tmp/a.go"}`}
		result := &message.ToolResult{ToolCallID: "x", Metadata: string(metaJSON)}
		item := newTestBaseTool(&sty, toolCall, result)
		out := item.formatEditResultForCopy()
		require.Contains(t, out, "Changes: +")
		require.Contains(t, out, "```diff")
	})

	t.Run("valid_metadata_no_content_change_is_empty", func(t *testing.T) {
		t.Parallel()
		meta := tools.EditResponseMetadata{}
		metaJSON, err := json.Marshal(meta)
		require.NoError(t, err)
		result := &message.ToolResult{ToolCallID: "x", Metadata: string(metaJSON)}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Edit}, result)
		require.Equal(t, "", item.formatEditResultForCopy())
	})
}

func TestFormatMultiEditResultForCopy(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	t.Run("nil_result", func(t *testing.T) {
		t.Parallel()
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.MultiEdit}, nil)
		require.Equal(t, "", item.formatMultiEditResultForCopy())
	})

	t.Run("no_metadata_falls_back_to_content", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "raw"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.MultiEdit}, result)
		require.Equal(t, "raw", item.formatMultiEditResultForCopy())
	})

	t.Run("invalid_metadata_falls_back_to_content", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "raw", Metadata: "not json"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.MultiEdit}, result)
		require.Equal(t, "raw", item.formatMultiEditResultForCopy())
	})

	t.Run("valid_metadata_with_content_shows_diff", func(t *testing.T) {
		t.Parallel()
		meta := tools.MultiEditResponseMetadata{OldContent: "a\n", NewContent: "b\n"}
		metaJSON, err := json.Marshal(meta)
		require.NoError(t, err)
		toolCall := message.ToolCall{Name: toolnames.MultiEdit, Input: `{"file_path":"/tmp/a.go"}`}
		result := &message.ToolResult{ToolCallID: "x", Metadata: string(metaJSON)}
		item := newTestBaseTool(&sty, toolCall, result)
		out := item.formatMultiEditResultForCopy()
		require.Contains(t, out, "Changes: +")
		require.Contains(t, out, "```diff")
	})

	t.Run("valid_metadata_no_content_change_is_empty", func(t *testing.T) {
		t.Parallel()
		meta := tools.MultiEditResponseMetadata{}
		metaJSON, err := json.Marshal(meta)
		require.NoError(t, err)
		result := &message.ToolResult{ToolCallID: "x", Metadata: string(metaJSON)}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.MultiEdit}, result)
		require.Equal(t, "", item.formatMultiEditResultForCopy())
	})
}

func TestFormatWriteResultForCopy(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	t.Run("nil_result", func(t *testing.T) {
		t.Parallel()
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Write, Input: `{}`}, nil)
		require.Equal(t, "", item.formatWriteResultForCopy())
	})

	t.Run("invalid_input_falls_back_to_content", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "raw"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Write, Input: "not json"}, result)
		require.Equal(t, "raw", item.formatWriteResultForCopy())
	})

	for _, tt := range fileLangExtensionCases {
		t.Run("lang_"+tt.path, func(t *testing.T) {
			t.Parallel()
			input, err := json.Marshal(tools.WriteParams{FilePath: tt.path, Content: "body"})
			require.NoError(t, err)
			toolCall := message.ToolCall{Name: toolnames.Write, Input: string(input)}
			result := &message.ToolResult{ToolCallID: "x"}
			item := newTestBaseTool(&sty, toolCall, result)
			out := item.formatWriteResultForCopy()
			require.Contains(t, out, "File:")
			if tt.lang != "" {
				require.Contains(t, out, "```"+tt.lang)
			}
			require.Contains(t, out, "body")
		})
	}
}

func TestFormatFetchResultForCopy(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	t.Run("nil_result", func(t *testing.T) {
		t.Parallel()
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Fetch}, nil)
		require.Equal(t, "", item.formatFetchResultForCopy())
	})

	t.Run("invalid_input_falls_back", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "raw"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Fetch, Input: "bad"}, result)
		require.Equal(t, "raw", item.formatFetchResultForCopy())
	})

	t.Run("full_params", func(t *testing.T) {
		t.Parallel()
		input, err := json.Marshal(tools.FetchParams{URL: "http://x", Format: "markdown", Timeout: 10})
		require.NoError(t, err)
		toolCall := message.ToolCall{Name: toolnames.Fetch, Input: string(input)}
		result := &message.ToolResult{ToolCallID: "x", Content: "body"}
		item := newTestBaseTool(&sty, toolCall, result)
		out := item.formatFetchResultForCopy()
		require.Contains(t, out, "URL: http://x")
		require.Contains(t, out, "Format: markdown")
		require.Contains(t, out, "Timeout: 10s")
		require.Contains(t, out, "body")
	})
}

func TestFormatWebFetchResultForCopy(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	t.Run("nil_result", func(t *testing.T) {
		t.Parallel()
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.WebFetch}, nil)
		require.Equal(t, "", item.formatWebFetchResultForCopy())
	})

	t.Run("invalid_input_falls_back", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "raw"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.WebFetch, Input: "bad"}, result)
		require.Equal(t, "raw", item.formatWebFetchResultForCopy())
	})

	t.Run("valid", func(t *testing.T) {
		t.Parallel()
		input, err := json.Marshal(tools.WebFetchParams{URL: "http://x"})
		require.NoError(t, err)
		toolCall := message.ToolCall{Name: toolnames.WebFetch, Input: string(input)}
		result := &message.ToolResult{ToolCallID: "x", Content: "# hi"}
		item := newTestBaseTool(&sty, toolCall, result)
		out := item.formatWebFetchResultForCopy()
		require.Contains(t, out, "URL: http://x")
		require.Contains(t, out, "```markdown")
		require.Contains(t, out, "# hi")
	})
}

func TestFormatAgentResultForCopy(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	t.Run("nil_result", func(t *testing.T) {
		t.Parallel()
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Agent}, nil)
		require.Equal(t, "", item.formatAgentResultForCopy())
	})

	t.Run("empty_content", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Agent}, result)
		require.Equal(t, "", item.formatAgentResultForCopy())
	})

	t.Run("with_content", func(t *testing.T) {
		t.Parallel()
		result := &message.ToolResult{ToolCallID: "x", Content: "summary text"}
		item := newTestBaseTool(&sty, message.ToolCall{Name: toolnames.Agent}, result)
		out := item.formatAgentResultForCopy()
		require.Contains(t, out, "```markdown")
		require.Contains(t, out, "summary text")
	})
}

func TestFormatToolForCopy(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	t.Run("pending", func(t *testing.T) {
		t.Parallel()
		toolCall := message.ToolCall{Name: toolnames.Bash, Input: `{"command":"echo hi"}`}
		item := newTestBaseTool(&sty, toolCall, nil)
		out := item.formatToolForCopy()
		require.Contains(t, out, "Bash Tool Call")
		require.Contains(t, out, "### Parameters:")
		require.Contains(t, out, "Pending...")
	})

	t.Run("canceled", func(t *testing.T) {
		t.Parallel()
		toolCall := message.ToolCall{Name: toolnames.Bash, Input: `{"command":"echo hi"}`}
		item := newBaseToolMessageItem(&sty, toolCall, nil, nil, true)
		out := item.formatToolForCopy()
		require.Contains(t, out, "Cancelled")
	})

	t.Run("error_result", func(t *testing.T) {
		t.Parallel()
		toolCall := message.ToolCall{Name: toolnames.Bash, Input: `{"command":"echo hi"}`}
		result := &message.ToolResult{ToolCallID: "x", IsError: true, Content: "boom"}
		item := newTestBaseTool(&sty, toolCall, result)
		out := item.formatToolForCopy()
		require.Contains(t, out, "### Error:")
		require.Contains(t, out, "boom")
	})

	t.Run("success_result", func(t *testing.T) {
		t.Parallel()
		toolCall := message.ToolCall{Name: toolnames.Bash, Input: `{"command":"echo hi"}`}
		meta := tools.BashResponseMetadata{Output: "hi"}
		metaJSON, err := json.Marshal(meta)
		require.NoError(t, err)
		result := &message.ToolResult{ToolCallID: "x", Metadata: string(metaJSON)}
		item := newTestBaseTool(&sty, toolCall, result)
		out := item.formatToolForCopy()
		require.Contains(t, out, "### Result:")
		require.Contains(t, out, "hi")
	})

	t.Run("no_input_skips_parameters_section", func(t *testing.T) {
		t.Parallel()
		toolCall := message.ToolCall{Name: toolnames.Bash}
		item := newTestBaseTool(&sty, toolCall, nil)
		out := item.formatToolForCopy()
		require.NotContains(t, out, "### Parameters:")
	})
}

func TestPrettifyToolName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{toolnames.Agent, toolnames.Agent},
		{toolnames.Bash, toolnames.Bash},
		{toolnames.JobOutput, "Job: Output"},
		{toolnames.JobKill, "Job: Kill"},
		{toolnames.Download, toolnames.Download},
		{toolnames.Edit, toolnames.Edit},
		{toolnames.MultiEdit, "Multi-Edit"},
		{toolnames.Fetch, toolnames.Fetch},
		{toolnames.WebFetch, "Fetch"},
		{toolnames.WebSearch, "Search"},
		{toolnames.Glob, toolnames.Glob},
		{toolnames.Grep, toolnames.Grep},
		{toolnames.LS, "List"},
		{toolnames.Sourcegraph, toolnames.Sourcegraph},
		{toolnames.Todos, "To-Do"},
		{toolnames.View, toolnames.View},
		{toolnames.Write, toolnames.Write},
		{"custom_tool_name", "Custom Tool Name"},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, prettifyToolName(tt.name), "for %s", tt.name)
	}
}

// agentParamsSmokeTest guards against a struct field rename in
// agent.AgentParams silently breaking the Agent branch of
// formatParametersForCopy: json.Marshal here must match the field the
// production code reads.
func TestFormatParametersForCopy_AgentUsesPromptField(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	input, err := json.Marshal(agent.AgentParams{Prompt: "investigate the bug"})
	require.NoError(t, err)
	toolCall := message.ToolCall{Name: toolnames.Agent, Input: string(input)}
	item := newTestBaseTool(&sty, toolCall, nil)
	require.Contains(t, item.formatParametersForCopy(), "investigate the bug")
}

func TestGetDigits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		n    int
		want int
	}{
		{0, 1},
		{5, 1},
		{10, 2},
		{999, 3},
		{-50, 2},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, getDigits(tt.n), "for %d", tt.n)
	}
}

// toolIcon is a second status-to-glyph mapping (independent of
// toolStatusStyle); every status must still resolve to a non-empty icon.
func TestToolIcon_AllStatuses(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	for _, status := range []ToolStatus{
		ToolStatusSuccess, ToolStatusError, ToolStatusCanceled,
		ToolStatusRunning, ToolStatusAwaitingPermission,
	} {
		require.NotEmpty(t, toolIcon(&sty, status), "status %v", status)
	}
}

func TestBaseToolMessageItem_SetCompact_NoOpWhenUnchanged(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "sym-4", Name: toolnames.LSPSymbols, Input: "{}", Finished: true}
	item := NewSymbolsToolMessageItem(&sty, toolCall, nil, false)
	base := item.(*baseToolMessageItem)

	versionBefore := base.Version()
	base.SetCompact(false) // already false: must be a no-op.
	require.Equal(t, versionBefore, base.Version())

	base.SetCompact(true)
	require.NotEqual(t, versionBefore, base.Version())
}

func TestBaseToolMessageItem_SetStatus_NoOpWhenUnchanged(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "sym-5", Name: toolnames.LSPSymbols, Input: "{}", Finished: true}
	item := NewSymbolsToolMessageItem(&sty, toolCall, nil, false)
	base := item.(*baseToolMessageItem)

	versionBefore := base.Version()
	base.SetStatus(ToolStatusRunning) // already the default: must be a no-op.
	require.Equal(t, versionBefore, base.Version())

	base.SetStatus(ToolStatusError)
	require.NotEqual(t, versionBefore, base.Version())
}

func TestPendingTool_NestedStyling(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	top := ansi.Strip(pendingTool(&sty, "Grep", nil, false))
	nested := ansi.Strip(pendingTool(&sty, "Grep", nil, true))
	require.Contains(t, top, "Grep")
	require.Contains(t, nested, "Grep")
}

func TestToolOutputDiffContent(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	out := ansi.Strip(toolOutputDiffContent(&sty, "a.go", "old\n", "new\n", 100, false))
	require.Contains(t, out, "new")

	wide := ansi.Strip(toolOutputDiffContent(&sty, "a.go", "old\n", "new\n", 200, false))
	require.Contains(t, wide, "new")

	var oldB, newB strings.Builder
	for i := range 30 {
		fmt.Fprintf(&oldB, "old %d\n", i)
		fmt.Fprintf(&newB, "new %d\n", i)
	}
	truncated := ansi.Strip(toolOutputDiffContent(&sty, "a.go", oldB.String(), newB.String(), 100, false))
	require.Contains(t, truncated, "hidden")

	expanded := ansi.Strip(toolOutputDiffContent(&sty, "a.go", oldB.String(), newB.String(), 100, true))
	require.NotContains(t, expanded, "hidden")
}
