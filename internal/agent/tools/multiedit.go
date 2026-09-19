package tools

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/diff"
	"github.com/NaturalSelect/angela/internal/filepathext"
	"github.com/NaturalSelect/angela/internal/filetracker"
	"github.com/NaturalSelect/angela/internal/fsext"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

type MultiEditOperation struct {
	OldString  string `json:"old_string" description:"The text to replace"`
	NewString  string `json:"new_string" description:"The text to replace it with"`
	ReplaceAll bool   `json:"replace_all,omitempty" description:"Replace all occurrences of old_string (default false)."`
}

type MultiEditParams struct {
	FilePath string               `json:"file_path" description:"The absolute path to the file to modify"`
	Edits    []MultiEditOperation `json:"edits" description:"Array of edit operations to perform sequentially on the file"`
}

type MultiEditPermissionsParams struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

// DiffPreview lets the permission package offer this batch of edits to
// an external code editor for review, without importing this package.
func (p MultiEditPermissionsParams) DiffPreview() (filePath, oldContent, newContent string) {
	return p.FilePath, p.OldContent, p.NewContent
}

type FailedEdit struct {
	Index int                `json:"index"`
	Error string             `json:"error"`
	Edit  MultiEditOperation `json:"edit"`
}

type MultiEditResponseMetadata struct {
	Additions    int          `json:"additions"`
	Removals     int          `json:"removals"`
	OldContent   string       `json:"old_content,omitempty"`
	NewContent   string       `json:"new_content,omitempty"`
	EditsApplied int          `json:"edits_applied"`
	EditsFailed  []FailedEdit `json:"edits_failed,omitempty"`
	// Created reports whether the batch created FilePath rather than
	// rewriting it, so a later undo knows to delete it instead of
	// restoring OldContent (which is empty either way).
	Created bool `json:"created,omitempty"`
}

//go:embed multiedit.md
var multieditDescription string

type multiEditTool struct {
	fantasy.AgentTool

	lspManager  *lsp.Manager
	files       history.Service
	filetracker filetracker.Service
	workingDir  string
}

func NewMultiEditTool(
	lspManager *lsp.Manager,
	files history.Service,
	filetracker filetracker.Service,
	workingDir string,
) fantasy.AgentTool {
	t := &multiEditTool{
		lspManager:  lspManager,
		files:       files,
		filetracker: filetracker,
		workingDir:  workingDir,
	}
	t.AgentTool = NewTool(toolnames.MultiEdit, multieditDescription, t.run)
	return t
}

func (t *multiEditTool) run(ctx context.Context, params MultiEditParams, call fantasy.ToolCall) Result {
	plan := t.plan(ctx, params)
	if plan.Response != nil {
		return *plan.Response
	}
	return plan.Apply(ctx)
}

func (t *multiEditTool) Plan(ctx context.Context, call fantasy.ToolCall) (Plan, error) {
	params, ok := decodeInput[MultiEditParams](call.Input)
	if !ok {
		return settled(Fail(fmt.Sprintf("invalid input for %s", toolnames.MultiEdit))), nil
	}
	return t.plan(ctx, params), nil
}

// plan runs every edit against an in-memory copy of the file, so the
// user sees the combined result of the whole batch rather than being
// asked once per edit.
func (t *multiEditTool) plan(ctx context.Context, params MultiEditParams) Plan {
	if params.FilePath == "" {
		return settled(Fail("file_path is required"))
	}
	if len(params.Edits) == 0 {
		return settled(Fail("at least one edit operation is required"))
	}

	params.FilePath = filepathext.SmartJoin(t.workingDir, params.FilePath)

	if err := validateEdits(params.Edits); err != nil {
		return settled(Fail(err.Error()))
	}

	edit := editContext{ctx, t.files, t.filetracker, t.workingDir}
	if params.Edits[0].OldString == "" {
		return t.planWithCreation(edit, params)
	}
	return t.planExistingFile(edit, params)
}

func validateEdits(edits []MultiEditOperation) error {
	for i, edit := range edits {
		// Only the first edit can have empty old_string (for file creation)
		if i > 0 && edit.OldString == "" {
			return fmt.Errorf("edit %d: only the first edit can have empty old_string (for file creation)", i+1)
		}
	}
	return nil
}

// applyEditsToContent applies edits sequentially, collecting the ones that
// failed. It also reports whether any edit only matched after whitespace
// normalization.
func applyEditsToContent(currentContent string, edits []MultiEditOperation, startIndex int) (string, []FailedEdit, bool) {
	var failedEdits []FailedEdit
	var whitespaceCorrected bool
	for i, edit := range edits {
		newContent, corrected, err := applyEditToContent(currentContent, edit)
		if err != nil {
			failedEdits = append(failedEdits, FailedEdit{
				Index: startIndex + i + 1,
				Error: err.Error(),
				Edit:  edit,
			})
			continue
		}
		whitespaceCorrected = whitespaceCorrected || corrected
		currentContent = newContent
	}
	return currentContent, failedEdits, whitespaceCorrected
}

func (t *multiEditTool) planWithCreation(edit editContext, params MultiEditParams) Plan {
	if _, err := os.Stat(params.FilePath); err == nil {
		return settled(Fail(fmt.Sprintf("file already exists: %s", params.FilePath)))
	} else if !os.IsNotExist(err) {
		return settled(FailErr("failed to access file", err))
	}

	firstEdit := params.Edits[0]
	currentContent, failedEdits, whitespaceCorrected := applyEditsToContent(firstEdit.NewString, params.Edits[1:], 1)

	sessionID := GetSessionFromContext(edit.ctx)
	if sessionID == "" {
		return settled(Fail("session ID is required for creating a new file"))
	}

	_, additions, removals := diff.GenerateDiff("", currentContent, strings.TrimPrefix(params.FilePath, t.workingDir))

	editsApplied := len(params.Edits) - len(failedEdits)
	description := fmt.Sprintf("Create file %s with %d edits", params.FilePath, editsApplied)
	if len(failedEdits) > 0 {
		description = fmt.Sprintf("Create file %s with %d of %d edits (%d failed)", params.FilePath, editsApplied, len(params.Edits), len(failedEdits))
	}

	metadata := MultiEditResponseMetadata{
		OldContent:   "",
		NewContent:   currentContent,
		Additions:    additions,
		Removals:     removals,
		EditsApplied: editsApplied,
		EditsFailed:  failedEdits,
		Created:      true,
	}

	return Plan{
		Preview: permission.Preview{
			Description: description,
			Params: MultiEditPermissionsParams{
				FilePath:   params.FilePath,
				OldContent: "",
				NewContent: currentContent,
			},
		},
		Refusal: metadata,
		Apply: func(ctx context.Context) Result {
			// Creating the parent directories belongs here rather than
			// in planning: a refused edit must leave no trace.
			if err := os.MkdirAll(filepath.Dir(params.FilePath), 0o755); err != nil {
				return FailErr("failed to create parent directories", err)
			}
			if err := os.WriteFile(params.FilePath, []byte(currentContent), 0o644); err != nil {
				return FailErr("failed to write file", err)
			}

			if _, err := t.files.Create(ctx, sessionID, params.FilePath, ""); err != nil {
				return FailErr("error creating file history", err)
			}
			if _, err := t.files.CreateVersion(ctx, sessionID, params.FilePath, currentContent); err != nil {
				slog.Error("Error creating file history version", "error", err)
			}
			t.filetracker.RecordRead(ctx, sessionID, params.FilePath)

			message := fmt.Sprintf("File created with %d edits: %s", len(params.Edits), params.FilePath)
			if len(failedEdits) > 0 {
				message = fmt.Sprintf("File created with %d of %d edits: %s (%d edit(s) failed)", editsApplied, len(params.Edits), params.FilePath, len(failedEdits))
			}
			return FromResponse(t.finish(ctx, params.FilePath, withWhitespaceNote(message, whitespaceCorrected), metadata))
		},
	}
}

func (t *multiEditTool) planExistingFile(edit editContext, params MultiEditParams) Plan {
	sessionID, oldContent, isCrlf, result, ok := loadExistingFile(edit, params.FilePath, "session ID is required for editing a file")
	if !ok {
		return settled(result)
	}

	currentContent, failedEdits, whitespaceCorrected := applyEditsToContent(oldContent, params.Edits, 0)

	if oldContent == currentContent {
		if len(failedEdits) > 0 {
			return settled(Fail(fmt.Sprintf("no changes made - all %d edit(s) failed", len(failedEdits))).WithMetadata(
				MultiEditResponseMetadata{
					EditsApplied: 0,
					EditsFailed:  failedEdits,
				},
			))
		}
		return settled(Fail("no changes made - all edits resulted in identical content"))
	}

	_, additions, removals := diff.GenerateDiff(oldContent, currentContent, strings.TrimPrefix(params.FilePath, t.workingDir))

	editsApplied := len(params.Edits) - len(failedEdits)
	description := fmt.Sprintf("Apply %d edits to file %s", editsApplied, params.FilePath)
	if len(failedEdits) > 0 {
		description = fmt.Sprintf("Apply %d of %d edits to file %s (%d failed)", editsApplied, len(params.Edits), params.FilePath, len(failedEdits))
	}

	metadata := MultiEditResponseMetadata{
		OldContent:   oldContent,
		NewContent:   currentContent,
		Additions:    additions,
		Removals:     removals,
		EditsApplied: editsApplied,
		EditsFailed:  failedEdits,
	}

	writeContent := currentContent
	if isCrlf {
		writeContent, _ = fsext.ToWindowsLineEndings(writeContent)
	}

	return Plan{
		Preview: permission.Preview{
			Description: description,
			Params: MultiEditPermissionsParams{
				FilePath:   params.FilePath,
				OldContent: oldContent,
				NewContent: currentContent,
			},
		},
		Refusal: metadata,
		Apply: func(ctx context.Context) Result {
			applyCtx := edit
			applyCtx.ctx = ctx
			if err := commitFileChange(applyCtx, sessionID, params.FilePath, oldContent, writeContent); err != nil {
				return Fail(err.Error())
			}

			message := fmt.Sprintf("Applied %d edits to file: %s", len(params.Edits), params.FilePath)
			if len(failedEdits) > 0 {
				message = fmt.Sprintf("Applied %d of %d edits to file: %s (%d edit(s) failed)", editsApplied, len(params.Edits), params.FilePath, len(failedEdits))
			}
			return FromResponse(t.finish(ctx, params.FilePath, withWhitespaceNote(message, whitespaceCorrected), metadata))
		},
	}
}

// finish tells the language servers what changed and folds the fresh
// diagnostics into the answer the model reads.
func (t *multiEditTool) finish(ctx context.Context, filePath, message string, metadata MultiEditResponseMetadata) fantasy.ToolResponse {
	notifyLSPs(ctx, t.lspManager, filePath)
	text := fmt.Sprintf("<result>\n%s\n</result>\n", message)
	text += getDiagnostics(filePath, t.lspManager)
	return fantasy.WithResponseMetadata(fantasy.NewTextResponse(text), metadata)
}

// applyEditToContent applies a single edit, reporting whether it only matched
// after whitespace normalization.
func applyEditToContent(content string, edit MultiEditOperation) (string, bool, error) {
	if edit.OldString == "" && edit.NewString == "" {
		return content, false, nil
	}

	if edit.OldString == "" {
		return "", false, fmt.Errorf("old_string cannot be empty for content replacement")
	}

	return findAndReplace(content, edit.OldString, edit.NewString, edit.ReplaceAll)
}
