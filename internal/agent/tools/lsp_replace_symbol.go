package tools

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"strings"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/diff"
	"github.com/NaturalSelect/angela/internal/filetracker"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

type ReplaceSymbolParams struct {
	Symbol      string `json:"symbol" description:"The symbol name to target (e.g., function name, method name, type name)"`
	FilePath    string `json:"file_path" description:"The path to the file containing the symbol"`
	Replacement string `json:"replacement,omitempty" description:"The replacement text. Required for 'replace' action. For 'add_before'/'add_after', the text to insert. Ignored for 'delete'."`
	Action      string `json:"action,omitempty" description:"Operation to perform: 'replace' (default, replace entire symbol), 'add_before' (insert before symbol), 'add_after' (insert after symbol), 'delete' (remove symbol entirely)"`
}

//go:embed lsp_replace_symbol.md
var replaceSymbolDescription string

// ReplaceSymbolResponseMetadata carries diff data for the renderer.
type ReplaceSymbolResponseMetadata struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content"`
	NewContent string `json:"new_content"`
	Action     string `json:"action"`
	Additions  int    `json:"additions"`
	Removals   int    `json:"removals"`
}

// ReplaceSymbolPermissionsParams carries diff data for the permission dialog.
type ReplaceSymbolPermissionsParams struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content"`
	NewContent string `json:"new_content"`
}

type replaceSymbolTool struct {
	fantasy.AgentTool

	lspManager  *lsp.Manager
	files       history.Service
	filetracker filetracker.Service
}

func NewReplaceSymbolTool(
	lspManager *lsp.Manager,
	files history.Service,
	filetracker filetracker.Service,
) fantasy.AgentTool {
	t := &replaceSymbolTool{
		lspManager:  lspManager,
		files:       files,
		filetracker: filetracker,
	}
	t.AgentTool = fantasy.NewAgentTool(toolnames.LSPReplaceSymbol, replaceSymbolDescription, t.run)
	return t
}

func (t *replaceSymbolTool) run(ctx context.Context, params ReplaceSymbolParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	plan, err := t.plan(ctx, params)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if plan.Response != nil {
		return *plan.Response, nil
	}
	return plan.Apply(ctx)
}

func (t *replaceSymbolTool) Plan(ctx context.Context, call fantasy.ToolCall) (Plan, error) {
	params, ok := decodeInput[ReplaceSymbolParams](call.Input)
	if !ok {
		return Plan{}, fmt.Errorf("invalid input for %s", toolnames.LSPReplaceSymbol)
	}
	return t.plan(ctx, params)
}

// plan locates the symbol and works out the file's new content. Every
// query it makes is read-only: the language server is only asked where
// the symbol is, and the file is only read.
func (t *replaceSymbolTool) plan(ctx context.Context, params ReplaceSymbolParams) (Plan, error) {
	if params.Symbol == "" {
		return settled(fantasy.NewTextErrorResponse("symbol is required")), nil
	}
	if params.FilePath == "" {
		return settled(fantasy.NewTextErrorResponse("file_path is required")), nil
	}

	action := params.Action
	if action == "" {
		action = "replace"
	}
	switch action {
	case "replace", "add_before", "add_after", "delete":
	default:
		return settled(fantasy.NewTextErrorResponse(fmt.Sprintf("invalid action %q: must be replace, add_before, add_after, or delete", action))), nil
	}
	if action != "delete" && params.Replacement == "" {
		return settled(fantasy.NewTextErrorResponse(fmt.Sprintf("replacement is required for action %q", action))), nil
	}

	t.lspManager.Start(ctx, params.FilePath)

	client := findLSPClient(t.lspManager, params.FilePath)
	if client == nil {
		return settled(fantasy.NewTextErrorResponse(fmt.Sprintf("no LSP client handles file: %s", params.FilePath))), nil
	}

	symbols, err := client.DocumentSymbols(ctx, params.FilePath)
	if err != nil {
		return settled(fantasy.NewTextErrorResponse(fmt.Sprintf("failed to get document symbols: %s", err))), nil
	}

	target := findSymbolByName(symbols, params.Symbol)
	if target == nil {
		return settled(fantasy.NewTextErrorResponse(fmt.Sprintf("symbol '%s' not found in %s", params.Symbol, params.FilePath))), nil
	}

	rng := target.GetRange()

	content, err := os.ReadFile(params.FilePath)
	if err != nil {
		return Plan{}, fmt.Errorf("failed to read file: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	startLine := int(rng.Start.Line)
	endLine := int(rng.End.Line)
	if startLine >= len(lines) || endLine >= len(lines) {
		return settled(fantasy.NewTextErrorResponse("symbol range exceeds file length")), nil
	}

	newContent := spliceSymbol(lines, action, params.Replacement, startLine, endLine)
	oldContent := string(content)
	sessionID := GetSessionFromContext(ctx)

	return Plan{
		Preview: permission.Preview{
			Description: fmt.Sprintf("%s symbol '%s' in %s", action, params.Symbol, params.FilePath),
			Params: ReplaceSymbolPermissionsParams{
				FilePath:   params.FilePath,
				OldContent: oldContent,
				NewContent: newContent,
			},
		},
		Apply: func(ctx context.Context) (fantasy.ToolResponse, error) {
			return t.apply(ctx, params, action, sessionID, oldContent, newContent, startLine, endLine)
		},
	}, nil
}

// spliceSymbol rebuilds the file's lines with the symbol's range
// replaced, surrounded or removed.
func spliceSymbol(lines []string, action, replacement string, startLine, endLine int) string {
	inserted := strings.Split(replacement, "\n")

	var newLines []string
	switch action {
	case "replace":
		newLines = make([]string, 0, len(lines))
		newLines = append(newLines, lines[:startLine]...)
		newLines = append(newLines, inserted...)
		newLines = append(newLines, lines[endLine+1:]...)
	case "add_before":
		newLines = make([]string, 0, len(lines)+len(inserted))
		newLines = append(newLines, lines[:startLine]...)
		newLines = append(newLines, inserted...)
		newLines = append(newLines, lines[startLine:]...)
	case "add_after":
		newLines = make([]string, 0, len(lines)+len(inserted))
		newLines = append(newLines, lines[:endLine+1]...)
		newLines = append(newLines, inserted...)
		newLines = append(newLines, lines[endLine+1:]...)
	case "delete":
		newLines = make([]string, 0, len(lines))
		newLines = append(newLines, lines[:startLine]...)
		newLines = append(newLines, lines[endLine+1:]...)
	}
	return strings.Join(newLines, "\n")
}

func (t *replaceSymbolTool) apply(
	ctx context.Context,
	params ReplaceSymbolParams,
	action, sessionID, oldContent, newContent string,
	startLine, endLine int,
) (fantasy.ToolResponse, error) {
	recordFileVersions(ctx, t.files, sessionID, params.FilePath, oldContent, newContent)

	if err := os.WriteFile(params.FilePath, []byte(newContent), 0o644); err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("failed to write file: %w", err)
	}

	if t.filetracker != nil && sessionID != "" {
		t.filetracker.RecordRead(ctx, sessionID, params.FilePath)
	}

	notifyLSPs(ctx, t.lspManager, params.FilePath)

	var summary string
	switch action {
	case "replace":
		summary = fmt.Sprintf("Replaced symbol '%s' in %s (lines %d-%d)", params.Symbol, params.FilePath, startLine+1, endLine+1)
	case "add_before":
		summary = fmt.Sprintf("Inserted before symbol '%s' in %s (before line %d)", params.Symbol, params.FilePath, startLine+1)
	case "add_after":
		summary = fmt.Sprintf("Inserted after symbol '%s' in %s (after line %d)", params.Symbol, params.FilePath, endLine+1)
	case "delete":
		summary = fmt.Sprintf("Deleted symbol '%s' from %s (lines %d-%d)", params.Symbol, params.FilePath, startLine+1, endLine+1)
	}

	resp := fantasy.NewTextResponse(summary + "\n" + getDiagnostics(params.FilePath, t.lspManager))
	_, additions, removals := diff.GenerateDiff(oldContent, newContent, params.FilePath)
	resp = fantasy.WithResponseMetadata(resp, ReplaceSymbolResponseMetadata{
		FilePath:   params.FilePath,
		OldContent: oldContent,
		NewContent: newContent,
		Action:     action,
		Additions:  additions,
		Removals:   removals,
	})
	return resp, nil
}

// findSymbolByName searches for a symbol by name in the document symbol tree.
func findSymbolByName(symbols []protocol.DocumentSymbolResult, name string) protocol.DocumentSymbolResult {
	for _, sym := range symbols {
		if sym.GetName() == name {
			return sym
		}
		if ds, ok := sym.(*protocol.DocumentSymbol); ok && len(ds.Children) > 0 {
			children := make([]protocol.DocumentSymbolResult, len(ds.Children))
			for i := range ds.Children {
				children[i] = &ds.Children[i]
			}
			if found := findSymbolByName(children, name); found != nil {
				return found
			}
		}
	}
	return nil
}
