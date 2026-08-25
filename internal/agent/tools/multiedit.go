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
}

const MultiEditToolName = "multiedit"

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
	t.AgentTool = fantasy.NewAgentTool(MultiEditToolName, multieditDescription, t.run)
	return t
}

func (t *multiEditTool) run(ctx context.Context, params MultiEditParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	plan, err := t.plan(ctx, params)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if plan.Response != nil {
		return *plan.Response, nil
	}
	return plan.Apply(ctx)
}

func (t *multiEditTool) Plan(ctx context.Context, call fantasy.ToolCall) (Plan, error) {
	params, ok := decodeInput[MultiEditParams](call.Input)
	if !ok {
		return Plan{}, fmt.Errorf("invalid input for %s", MultiEditToolName)
	}
	return t.plan(ctx, params)
}

// plan runs every edit against an in-memory copy of the file, so the
// user sees the combined result of the whole batch rather than being
// asked once per edit.
func (t *multiEditTool) plan(ctx context.Context, params MultiEditParams) (Plan, error) {
	if params.FilePath == "" {
		return settled(fantasy.NewTextErrorResponse("file_path is required")), nil
	}
	if len(params.Edits) == 0 {
		return settled(fantasy.NewTextErrorResponse("at least one edit operation is required")), nil
	}

	params.FilePath = filepathext.SmartJoin(t.workingDir, params.FilePath)

	if err := validateEdits(params.Edits); err != nil {
		return settled(fantasy.NewTextErrorResponse(err.Error())), nil
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

func (t *multiEditTool) planWithCreation(edit editContext, params MultiEditParams) (Plan, error) {
	if _, err := os.Stat(params.FilePath); err == nil {
		return settled(fantasy.NewTextErrorResponse(fmt.Sprintf("file already exists: %s", params.FilePath))), nil
	} else if !os.IsNotExist(err) {
		return Plan{}, fmt.Errorf("failed to access file: %w", err)
	}

	firstEdit := params.Edits[0]
	currentContent, failedEdits, whitespaceCorrected := applyEditsToContent(firstEdit.NewString, params.Edits[1:], 1)

	sessionID := GetSessionFromContext(edit.ctx)
	if sessionID == "" {
		return Plan{}, fmt.Errorf("session ID is required for creating a new file")
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
		Apply: func(ctx context.Context) (fantasy.ToolResponse, error) {
			// Creating the parent directories belongs here rather than
			// in planning: a refused edit must leave no trace.
			if err := os.MkdirAll(filepath.Dir(params.FilePath), 0o755); err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to create parent directories: %w", err)
			}
			if err := os.WriteFile(params.FilePath, []byte(currentContent), 0o644); err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("failed to write file: %w", err)
			}

			if _, err := t.files.Create(ctx, sessionID, params.FilePath, ""); err != nil {
				return fantasy.ToolResponse{}, fmt.Errorf("error creating file history: %w", err)
			}
			if _, err := t.files.CreateVersion(ctx, sessionID, params.FilePath, currentContent); err != nil {
				slog.Error("Error creating file history version", "error", err)
			}
			t.filetracker.RecordRead(ctx, sessionID, params.FilePath)

			message := fmt.Sprintf("File created with %d edits: %s", len(params.Edits), params.FilePath)
			if len(failedEdits) > 0 {
				message = fmt.Sprintf("File created with %d of %d edits: %s (%d edit(s) failed)", editsApplied, len(params.Edits), params.FilePath, len(failedEdits))
			}
			return t.finish(ctx, params.FilePath, withWhitespaceNote(message, whitespaceCorrected), metadata), nil
		},
	}, nil
}

func (t *multiEditTool) planExistingFile(edit editContext, params MultiEditParams) (Plan, error) {
	sessionID, oldContent, isCrlf, resp, err := loadExistingFile(edit, params.FilePath, "session ID is required for editing a file")
	if err != nil {
		return Plan{}, err
	}
	if resp.Content != "" || resp.IsError {
		return settled(resp), nil
	}

	currentContent, failedEdits, whitespaceCorrected := applyEditsToContent(oldContent, params.Edits, 0)

	if oldContent == currentContent {
		if len(failedEdits) > 0 {
			return settled(fantasy.WithResponseMetadata(
				fantasy.NewTextErrorResponse(fmt.Sprintf("no changes made - all %d edit(s) failed", len(failedEdits))),
				MultiEditResponseMetadata{
					EditsApplied: 0,
					EditsFailed:  failedEdits,
				},
			)), nil
		}
		return settled(fantasy.NewTextErrorResponse("no changes made - all edits resulted in identical content")), nil
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
		Apply: func(ctx context.Context) (fantasy.ToolResponse, error) {
			applyCtx := edit
			applyCtx.ctx = ctx
			if err := commitFileChange(applyCtx, sessionID, params.FilePath, oldContent, writeContent); err != nil {
				return fantasy.ToolResponse{}, err
			}

			message := fmt.Sprintf("Applied %d edits to file: %s", len(params.Edits), params.FilePath)
			if len(failedEdits) > 0 {
				message = fmt.Sprintf("Applied %d of %d edits to file: %s (%d edit(s) failed)", editsApplied, len(params.Edits), params.FilePath, len(failedEdits))
			}
			return t.finish(ctx, params.FilePath, withWhitespaceNote(message, whitespaceCorrected), metadata), nil
		},
	}, nil
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
