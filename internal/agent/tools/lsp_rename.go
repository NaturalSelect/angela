package tools

import (
	"cmp"
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"charm.land/fantasy"

	"github.com/NaturalSelect/angela/internal/filetracker"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/lsp"
	lsputil "github.com/NaturalSelect/angela/internal/lsp/util"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

type RenameParams struct {
	Symbol  string `json:"symbol" description:"The symbol name to rename"`
	NewName string `json:"new_name" description:"The new name for the symbol"`
	Path    string `json:"path,omitempty" description:"The directory to search in. Defaults to the current working directory."`
}

// RenamePermissionsParams names the symbol change for the permission
// dialog. A rename rewrites every file holding a reference, so what the
// user needs to approve is the symbol going from one name to the other,
// not a diff of each file it touches.
type RenamePermissionsParams struct {
	Symbol  string `json:"symbol"`
	NewName string `json:"new_name"`
}

// RenameResponseMetadata records each renamed file's content before and
// after the edit, so a later undo can restore it without re-deriving
// the edit from the language server.
type RenameResponseMetadata struct {
	Files []RenamedFile `json:"files"`
}

type RenamedFile struct {
	Path       string `json:"path"`
	OldContent string `json:"old_content"`
	NewContent string `json:"new_content"`
}

//go:embed lsp_rename.md
var renameDescription string

type renameTool struct {
	fantasy.AgentTool

	lspManager  *lsp.Manager
	files       history.Service
	filetracker filetracker.Service
}

func NewRenameTool(
	lspManager *lsp.Manager,
	files history.Service,
	filetracker filetracker.Service,
) fantasy.AgentTool {
	t := &renameTool{
		lspManager:  lspManager,
		files:       files,
		filetracker: filetracker,
	}
	t.AgentTool = NewTool(toolnames.LSPRename, renameDescription, t.run)
	return t
}

func (t *renameTool) run(ctx context.Context, params RenameParams, call fantasy.ToolCall) Result {
	plan := t.plan(ctx, params)
	if plan.Response != nil {
		return *plan.Response
	}
	return plan.Apply(ctx)
}

func (t *renameTool) Plan(ctx context.Context, call fantasy.ToolCall) (Plan, error) {
	params, ok := decodeInput[RenameParams](call.Input)
	if !ok {
		return settled(Fail(fmt.Sprintf("invalid input for %s", toolnames.LSPRename))), nil
	}
	return t.plan(ctx, params), nil
}

// plan asks the language server what the rename would touch. That
// answer is a proposal, not a write: nothing reaches disk until Apply.
func (t *renameTool) plan(ctx context.Context, params RenameParams) Plan {
	if params.Symbol == "" {
		return settled(Fail("symbol is required"))
	}
	if params.NewName == "" {
		return settled(Fail("new_name is required"))
	}

	workingDir := cmp.Or(params.Path, ".")
	resolved, err := resolveSymbol(ctx, t.lspManager, params.Symbol, workingDir)
	if err != nil {
		return settled(Failf("Symbol '%s' not found", params.Symbol))
	}

	edit, err := resolved.client.Rename(ctx, resolved.path, resolved.line, resolved.char, params.NewName)
	if err != nil {
		slog.Error("Failed to rename symbol", "error", err, "symbol", params.Symbol)
		return settled(Failf("rename failed: %s", err))
	}
	if edit == nil {
		return settled(Ok(fmt.Sprintf("No rename edits generated for symbol '%s'", params.Symbol)))
	}

	affectedFiles := collectAffectedFiles(edit)
	encoding := resolved.client.GetOffsetEncoding()

	return Plan{
		Preview: permission.Preview{
			Description: fmt.Sprintf("Rename '%s' to '%s'", params.Symbol, params.NewName),
			Params: RenamePermissionsParams{
				Symbol:  params.Symbol,
				NewName: params.NewName,
			},
		},
		Apply: func(ctx context.Context) Result {
			sessionID := GetSessionFromContext(ctx)
			before := t.readAll(affectedFiles)

			if err := lsputil.ApplyWorkspaceEdit(*edit, encoding); err != nil {
				return Failf("failed to apply rename edits: %s", err)
			}

			renamed := t.recordRename(ctx, sessionID, affectedFiles, before)
			notifyLSPs(ctx, t.lspManager, "")

			var b strings.Builder
			fmt.Fprintf(&b, "Renamed '%s' to '%s' in %d file(s):\n\n", params.Symbol, params.NewName, len(affectedFiles))
			for _, f := range affectedFiles {
				fmt.Fprintf(&b, "  %s\n", f)
			}

			text := b.String()
			if len(affectedFiles) > 0 {
				text += "\n" + getDiagnostics(affectedFiles[0], t.lspManager)
			}
			return Ok(text).WithMetadata(RenameResponseMetadata{Files: renamed})
		},
	}
}

// readAll snapshots the files a rename is about to rewrite, so both
// sides of the change can be recorded once it lands.
func (t *renameTool) readAll(paths []string) map[string]string {
	before := make(map[string]string, len(paths))
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("Failed to read file for version tracking", "path", path, "error", err)
			continue
		}
		before[path] = string(content)
	}
	return before
}

func (t *renameTool) recordRename(ctx context.Context, sessionID string, paths []string, before map[string]string) []RenamedFile {
	if sessionID == "" {
		return nil
	}
	var renamed []RenamedFile
	for _, path := range paths {
		if t.filetracker != nil {
			t.filetracker.RecordRead(ctx, sessionID, path)
		}
		oldContent, ok := before[path]
		if !ok {
			continue
		}
		after, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("Failed to read renamed file for version tracking", "path", path, "error", err)
			continue
		}
		recordFileVersions(ctx, t.files, sessionID, path, oldContent, string(after))
		renamed = append(renamed, RenamedFile{Path: path, OldContent: oldContent, NewContent: string(after)})
	}
	return renamed
}
