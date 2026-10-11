package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/charmbracelet/x/ansi"
)

// spillingTool wraps a fantasy.AgentTool so a text result too large for the
// model's context is shown as its beginning and end, with the full text
// saved to a file the model can page through with Read or Grep. Without it
// the middle of an oversized result is lost for good, and the model can
// only recover it by re-running the tool.
type spillingTool struct {
	inner fantasy.AgentTool
	root  string
}

func newSpillingTool(inner fantasy.AgentTool, root string) *spillingTool {
	return &spillingTool{inner: inner, root: root}
}

// ToolOutputRoot is where spilled tool output lives under the data
// directory. The permission service must treat it as readable, so the
// writer and the app wiring share this one definition.
func ToolOutputRoot(dataDirectory string) string {
	return filepath.Join(dataDirectory, "tool-output")
}

// spillExempt lists the tools whose results must reach the model, or the
// stored report, in full. Read is how a spilled file is retrieved, so
// spilling its result would send the model after a file that holds the
// same text. Agent and LoadReport results are the stored reports that
// tools.CollectReports reads back verbatim. ProposalRead returns a
// proposal the model has to see whole. Merge carries the proposal into
// the branch's own stored result, the only copy that outlives the branch,
// so a preview there would leave the user a note pointing at a file that
// is pruned after a day.
var spillExempt = map[string]struct{}{
	toolnames.Read:         {},
	toolnames.Agent:        {},
	toolnames.LoadReport:   {},
	toolnames.ProposalRead: {},
	toolnames.Merge:        {},
}

// wrapToolsWithSpill wraps every tool that is not exempt. It must sit
// outside permissionedTool: that wrapper type-asserts on its inner tool to
// find the MCP identity and the Planner interface, which a wrapper in
// between would hide.
func wrapToolsWithSpill(agentTools []fantasy.AgentTool, root string) []fantasy.AgentTool {
	out := make([]fantasy.AgentTool, len(agentTools))
	for i, tool := range agentTools {
		if _, exempt := spillExempt[tool.Info().Name]; exempt {
			out[i] = tool
			continue
		}
		out[i] = newSpillingTool(tool, root)
	}
	return out
}

func (s *spillingTool) Info() fantasy.ToolInfo {
	return s.inner.Info()
}

func (s *spillingTool) ProviderOptions() fantasy.ProviderOptions {
	return s.inner.ProviderOptions()
}

func (s *spillingTool) SetProviderOptions(opts fantasy.ProviderOptions) {
	s.inner.SetProviderOptions(opts)
}

func (s *spillingTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	resp, err := s.inner.Run(ctx, call)
	if err != nil || resp.Type != "text" || !tools.ExceedsOutputLimit(resp.Content) {
		return resp, err
	}

	saved := wrapLongLines(resp.Content)
	path, spillErr := spillToolOutput(s.root, tools.GetSessionFromContext(ctx), saved)
	if spillErr != nil {
		slog.Warn("Failed to save oversized tool output", "tool", call.Name, "error", spillErr)
	}
	resp.Content = tools.TruncateOutput(resp.Content) + spillNotice(resp.Content, saved, path)
	return resp, nil
}

// wrapLongLines breaks lines too wide for Grep to show whole. Read also
// cuts long lines, at a byte limit, so a line Grep can show whole is one
// Read can show too. Without this a single huge line, such as minified
// JSON, would be unreachable past its first few hundred characters.
// Content with no such line is returned untouched.
func wrapLongLines(content string) string {
	for line := range strings.Lines(content) {
		if ansi.StringWidth(line) > tools.MaxGrepContentWidth {
			return ansi.Hardwrap(content, tools.MaxGrepContentWidth, true)
		}
	}
	return content
}

// spillToolOutput writes content to a new file under the session's
// directory in root and returns its path. Session directories left
// untouched past tools.SessionScratchRetention are pruned first.
func spillToolOutput(root, sessionID, content string) (string, error) {
	dir, err := tools.SessionScratchDir(root, sessionID)
	if err != nil {
		return "", err
	}
	if err := tools.PruneSessionScratch(root, tools.SessionScratchRetention); err != nil {
		slog.Warn("Failed to prune old tool output", "error", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating tool output directory: %w", err)
	}

	file, err := os.CreateTemp(dir, "output-*.txt")
	if err != nil {
		return "", fmt.Errorf("creating tool output file: %w", err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("writing tool output file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("closing tool output file: %w", err)
	}
	return file.Name(), nil
}

// spillNotice tells the model what it is looking at. original is what the
// tool returned and saved is what went to the file; they differ only by
// the line breaks wrapLongLines added. An empty path means the full text
// could not be saved.
func spillNotice(original, saved, path string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n\n[Output truncated: %d lines, %d characters in total; showing the beginning and end.", strings.Count(saved, "\n")+1, utf8.RuneCountInString(original))
	if path == "" {
		b.WriteString(" The full output could not be saved.]")
		return b.String()
	}
	fmt.Fprintf(&b, " Full output saved to %s. Use Read with offset and limit, or Grep with path set to that file, to see the rest.", path)
	if saved != original {
		fmt.Fprintf(&b, " Lines wider than %d columns were wrapped in the saved file.", tools.MaxGrepContentWidth)
	}
	b.WriteString(" Prefer reading that file over re-running the command.]")
	return b.String()
}
