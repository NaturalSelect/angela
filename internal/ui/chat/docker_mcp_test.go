package chat

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func dockerMCPCall(id, tool, input string) message.ToolCall {
	return message.ToolCall{
		ID:       id,
		Name:     toolnames.MCPPrefix + config.DockerMCPName + "_" + tool,
		Input:    input,
		Finished: true,
	}
}

func TestIsDockerMCPTool(t *testing.T) {
	t.Parallel()

	require.True(t, IsDockerMCPTool(toolnames.MCPPrefix+config.DockerMCPName+"_mcp-find"))
	require.False(t, IsDockerMCPTool(toolnames.MCPPrefix+"github_search"))
	require.False(t, IsDockerMCPTool("Bash"))
}

func TestDockerMCPToolPending(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-find", `{"query":"github"}`)
	toolCall.Finished = false
	item := NewDockerMCPToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Docker MCP")
	require.Contains(t, out, "Find")
}

func TestDockerMCPToolFindShowsQueryAndExtraArgs(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-find", `{"query":"github","limit":5}`)
	item := NewDockerMCPToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "github")
	require.Contains(t, out, "limit")
}

func TestDockerMCPToolAddShowsName(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-add", `{"name":"github","url":"https://example.com"}`)
	item := NewDockerMCPToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Add")
	require.Contains(t, out, "github")
}

func TestDockerMCPToolRemoveShowsName(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-remove", `{"name":"github"}`)
	item := NewDockerMCPToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Remove")
	require.Contains(t, out, "github")
}

func TestDockerMCPToolExecShowsName(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-exec", `{"name":"github","args":{}}`)
	item := NewDockerMCPToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Exec")
	require.Contains(t, out, "github")
}

func TestDockerMCPToolConfigSetShowsServer(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-config-set", `{"server":"github","key":"token","value":"x"}`)
	item := NewDockerMCPToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Config Set")
	require.Contains(t, out, "github")
}

func TestDockerMCPToolCodeModeFormatsName(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "code-mode", `{}`)
	item := NewDockerMCPToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Code Mode")
}

func TestDockerMCPToolUnknownToolHumanizesName(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "some_custom-tool", `{}`)
	item := NewDockerMCPToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Some Custom Tool")
}

func TestDockerMCPToolCompactShowsHeaderOnly(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-exec", `{"name":"github"}`)
	result := &message.ToolResult{ToolCallID: "m1", Content: "distinctive-exec-output"}
	item := NewDockerMCPToolMessageItem(&sty, toolCall, result, false)

	compactable, ok := item.(Compactable)
	require.True(t, ok, "tool items must implement Compactable")
	compactable.SetCompact(true)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive-exec-output")
}

func TestDockerMCPToolEarlyStateOnError(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-exec", `{"name":"github"}`)
	result := &message.ToolResult{ToolCallID: "m1", IsError: true, Content: "boom"}
	item := NewDockerMCPToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "boom")
}

func TestDockerMCPToolExecCollapsedHidesBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-exec", `{"name":"github"}`)
	result := &message.ToolResult{ToolCallID: "m1", Content: "distinctive-exec-output"}
	item := NewDockerMCPToolMessageItem(&sty, toolCall, result, false)

	out := ansi.Strip(item.Render(100))
	require.NotContains(t, out, "distinctive-exec-output")
}

func TestDockerMCPToolExecExpandedShowsTextBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-exec", `{"name":"github"}`)
	result := &message.ToolResult{ToolCallID: "m1", Content: "distinctive-exec-output"}
	item := NewDockerMCPToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "distinctive-exec-output")
}

func TestDockerMCPToolExecExpandedShowsImageBody(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-exec", `{"name":"github"}`)
	result := &message.ToolResult{ToolCallID: "m1", Data: "aGVsbG8=", MIMEType: "image/png"}
	item := NewDockerMCPToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := item.Render(100)
	require.NotEmpty(t, ansi.Strip(out))
}

func TestDockerMCPToolFindExpandedRendersServerTable(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-find", `{"query":"git"}`)
	result := &message.ToolResult{
		ToolCallID: "m1",
		Content:    `{"servers":[{"name":"github","description":"GitHub integration"},{"name":"gitlab","description":"GitLab integration"}]}`,
	}
	item := NewDockerMCPToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "github")
	require.Contains(t, out, "GitHub integration")
	require.Contains(t, out, "gitlab")
}

func TestDockerMCPToolFindExpandedShowsMoreCountBeyondTen(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	var servers []string
	for i := range 15 {
		servers = append(servers, `{"name":"server`+itoaLocal(i)+`","description":"desc"}`)
	}
	content := `{"servers":[` + strings.Join(servers, ",") + `]}`

	toolCall := dockerMCPCall("m1", "mcp-find", `{"query":"git"}`)
	result := &message.ToolResult{ToolCallID: "m1", Content: content}
	item := NewDockerMCPToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "and 5 more")
}

func TestDockerMCPToolFindExpandedShowsEmptyMessage(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-find", `{"query":"git"}`)
	result := &message.ToolResult{ToolCallID: "m1", Content: `{"servers":[]}`}
	item := NewDockerMCPToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "No MCP servers found.")
}

func TestDockerMCPToolFindExpandedFallsBackToPlainTextOnInvalidJSON(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-find", `{"query":"git"}`)
	result := &message.ToolResult{ToolCallID: "m1", Content: "not json at all"}
	item := NewDockerMCPToolMessageItem(&sty, toolCall, result, false)

	expandable, ok := item.(Expandable)
	require.True(t, ok, "tool items must implement Expandable")
	require.True(t, expandable.ToggleExpanded())

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "not json at all")
}

func TestDockerMCPToolInvalidInputParamsDoesNotPanic(t *testing.T) {
	t.Parallel()
	sty := styles.CharmtonePantera()

	toolCall := dockerMCPCall("m1", "mcp-find", `not-json`)
	item := NewDockerMCPToolMessageItem(&sty, toolCall, nil, false)

	out := ansi.Strip(item.Render(100))
	require.Contains(t, out, "Docker MCP")
}

// itoaLocal is a tiny stdlib-free integer formatter used only to build
// fixtures for the ">10 servers" test without pulling in fmt for a single
// call site already covered by dozens of other loops in this package.
func itoaLocal(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
