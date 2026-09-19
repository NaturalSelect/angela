package tools

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/diff"
	"github.com/NaturalSelect/angela/internal/filepathext"
	"github.com/NaturalSelect/angela/internal/filetracker"
	"github.com/NaturalSelect/angela/internal/fsext"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/toolnames"

	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/permission"
)

type EditParams struct {
	FilePath   string `json:"file_path" description:"The absolute path to the file to modify"`
	OldString  string `json:"old_string" description:"The text to replace"`
	NewString  string `json:"new_string" description:"The text to replace it with"`
	ReplaceAll bool   `json:"replace_all,omitempty" description:"Replace all occurrences of old_string (default false)"`
}

type EditPermissionsParams struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

// DiffPreview lets the permission package offer this edit to an
// external code editor for review, without importing this package.
func (p EditPermissionsParams) DiffPreview() (filePath, oldContent, newContent string) {
	return p.FilePath, p.OldContent, p.NewContent
}

type EditResponseMetadata struct {
	Additions  int    `json:"additions"`
	Removals   int    `json:"removals"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
	// Created reports whether the edit created filePath rather than
	// rewriting it, so a later undo knows to delete it instead of
	// restoring OldContent (which is empty either way).
	Created bool `json:"created,omitempty"`
}

//go:embed edit.md
var editDescription string

type editContext struct {
	ctx         context.Context
	files       history.Service
	filetracker filetracker.Service
	workingDir  string
}

type editTool struct {
	fantasy.AgentTool

	lspManager  *lsp.Manager
	files       history.Service
	filetracker filetracker.Service
	workingDir  string
}

func NewEditTool(
	lspManager *lsp.Manager,
	files history.Service,
	filetracker filetracker.Service,
	workingDir string,
) fantasy.AgentTool {
	t := &editTool{
		lspManager:  lspManager,
		files:       files,
		filetracker: filetracker,
		workingDir:  workingDir,
	}
	t.AgentTool = NewTool(toolnames.Edit, editDescription, t.run)
	return t
}

func (t *editTool) run(ctx context.Context, params EditParams, call fantasy.ToolCall) Result {
	plan := t.plan(ctx, params)
	if plan.Response != nil {
		return *plan.Response
	}
	return plan.Apply(ctx)
}

func (t *editTool) Plan(ctx context.Context, call fantasy.ToolCall) (Plan, error) {
	params, ok := decodeInput[EditParams](call.Input)
	if !ok {
		return settled(Fail(fmt.Sprintf("invalid input for %s", toolnames.Edit))), nil
	}
	return t.plan(ctx, params), nil
}

// plan works out the file's new content without writing anything. Which
// of the three shapes an edit takes is decided by what the caller left
// empty: no old string creates, no new string deletes.
func (t *editTool) plan(ctx context.Context, params EditParams) Plan {
	if params.FilePath == "" {
		return settled(Fail("file_path is required"))
	}

	filePath := filepathext.SmartJoin(t.workingDir, params.FilePath)
	edit := editContext{ctx, t.files, t.filetracker, t.workingDir}

	switch {
	case params.OldString == "":
		return t.planCreateNewFile(edit, filePath, params.NewString)
	case params.NewString == "":
		return t.planRewrite(edit, rewrite{
			filePath:   filePath,
			oldString:  params.OldString,
			replaceAll: params.ReplaceAll,
			describe:   "Delete content from file ",
			done:       "Content deleted from file: ",
			sessionErr: "session ID is required for deleting content",
		})
	default:
		return t.planRewrite(edit, rewrite{
			filePath:   filePath,
			oldString:  params.OldString,
			newString:  params.NewString,
			replaceAll: params.ReplaceAll,
			describe:   "Replace content in file ",
			done:       "Content replaced in file: ",
			sessionErr: "session ID is required for editing a file",
			rejectNoop: true,
		})
	}
}

func (t *editTool) planCreateNewFile(edit editContext, filePath, content string) Plan {
	fileInfo, err := os.Stat(filePath)
	if err == nil {
		if fileInfo.IsDir() {
			return settled(Fail(fmt.Sprintf("path is a directory, not a file: %s", filePath)))
		}
		return settled(Fail(fmt.Sprintf("file already exists: %s", filePath)))
	} else if !os.IsNotExist(err) {
		return settled(FailErr("failed to access file", err))
	}

	sessionID := GetSessionFromContext(edit.ctx)
	if sessionID == "" {
		return settled(Fail("session ID is required for creating a new file"))
	}

	_, additions, removals := diff.GenerateDiff(
		"",
		content,
		strings.TrimPrefix(filePath, t.workingDir),
	)
	metadata := EditResponseMetadata{
		OldContent: "",
		NewContent: content,
		Additions:  additions,
		Removals:   removals,
		Created:    true,
	}

	return Plan{
		Preview: permission.Preview{
			Description: fmt.Sprintf("Create file %s", filePath),
			Params: EditPermissionsParams{
				FilePath:   filePath,
				OldContent: "",
				NewContent: content,
			},
		},
		Refusal: metadata,
		Apply: func(ctx context.Context) Result {
			// Creating the parent directories belongs here rather than
			// in planning: a refused edit must leave no trace.
			if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
				return FailErr("failed to create parent directories", err)
			}
			if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
				return FailErr("failed to write file", err)
			}

			if _, err := t.files.Create(ctx, sessionID, filePath, ""); err != nil {
				return FailErr("error creating file history", err)
			}
			if _, err := t.files.CreateVersion(ctx, sessionID, filePath, content); err != nil {
				slog.Error("Error creating file history version", "error", err)
			}
			t.filetracker.RecordRead(ctx, sessionID, filePath)

			return FromResponse(t.finish(ctx, filePath, "File created: "+filePath, metadata))
		},
	}
}

// rewrite describes an edit to a file that already exists. Deleting is
// replacing with nothing, so both shapes share one body and differ only
// in what they call themselves.
type rewrite struct {
	filePath   string
	oldString  string
	newString  string
	replaceAll bool
	describe   string
	done       string
	sessionErr string
	// rejectNoop refuses an edit that would leave the file unchanged.
	// Deleting found content always changes it, so only replacing needs
	// the check.
	rejectNoop bool
}

func (t *editTool) planRewrite(edit editContext, r rewrite) Plan {
	sessionID, oldContent, isCrlf, result, ok := loadExistingFile(edit, r.filePath, r.sessionErr)
	if !ok {
		return settled(result)
	}

	newContent, whitespaceCorrected, err := findAndReplace(oldContent, r.oldString, r.newString, r.replaceAll)
	if err != nil {
		return settled(Fail(err.Error()))
	}
	if r.rejectNoop && newContent == oldContent {
		return settled(Fail("new content is the same as old content. No changes made."))
	}

	_, additions, removals := diff.GenerateDiff(
		oldContent,
		newContent,
		strings.TrimPrefix(r.filePath, t.workingDir),
	)

	writeContent := newContent
	if isCrlf {
		writeContent, _ = fsext.ToWindowsLineEndings(writeContent)
	}

	return Plan{
		Preview: permission.Preview{
			Description: r.describe + r.filePath,
			Params: EditPermissionsParams{
				FilePath:   r.filePath,
				OldContent: oldContent,
				NewContent: newContent,
			},
		},
		Refusal: EditResponseMetadata{
			OldContent: oldContent,
			NewContent: newContent,
			Additions:  additions,
			Removals:   removals,
		},
		Apply: func(ctx context.Context) Result {
			applyCtx := edit
			applyCtx.ctx = ctx
			if err := commitFileChange(applyCtx, sessionID, r.filePath, oldContent, writeContent); err != nil {
				return Fail(err.Error())
			}
			return FromResponse(t.finish(ctx, r.filePath,
				withWhitespaceNote(r.done+r.filePath, whitespaceCorrected),
				EditResponseMetadata{
					OldContent: oldContent,
					NewContent: writeContent,
					Additions:  additions,
					Removals:   removals,
				},
			))
		},
	}
}

// finish tells the language servers what changed and folds the fresh
// diagnostics into the answer the model reads.
func (t *editTool) finish(ctx context.Context, filePath, message string, metadata EditResponseMetadata) fantasy.ToolResponse {
	notifyLSPs(ctx, t.lspManager, filePath)
	text := fmt.Sprintf("<result>\n%s\n</result>\n", message)
	text += getDiagnostics(filePath, t.lspManager)
	return fantasy.WithResponseMetadata(fantasy.NewTextResponse(text), metadata)
}

// findAndReplace performs a find-and-replace on content. When replaceAll is
// false it requires exactly one match. If an exact match fails, it falls back
// to whitespace-normalized matching and, failing that, returns a diagnostic
// hint describing why the replacement could not be made. The returned boolean
// reports whether the replacement relied on the whitespace-normalized
// fallback rather than an exact match.
func findAndReplace(content, old, new string, replaceAll bool) (string, bool, error) {
	if replaceAll {
		if strings.Contains(content, old) {
			return strings.ReplaceAll(content, old, new), false, nil
		}
	} else {
		index := strings.Index(content, old)
		switch {
		case index == -1:
			// Fall through to the fuzzy fallback below.
		case index != strings.LastIndex(content, old):
			return "", false, fmt.Errorf("old_string appears multiple times in the file. Please provide more context to ensure a unique match, or set replace_all to true")
		default:
			return content[:index] + new + content[index+len(old):], false, nil
		}
	}

	if result, ok := normalizedReplace(content, old, new, replaceAll); ok {
		return result, true, nil
	}
	return "", false, notFoundError(content, old)
}

// withWhitespaceNote appends the whitespace auto-correction note to a tool
// response message when the edit did not match the file byte-for-byte.
func withWhitespaceNote(message string, whitespaceCorrected bool) string {
	if !whitespaceCorrected {
		return message
	}
	return message + "\n" + whitespaceCorrectedNote
}

// notFoundError builds the "old_string not found" error, appending a
// diagnostic hint when one is available to help the caller self-correct.
func notFoundError(content, old string) error {
	msg := "old_string not found in file. Make sure it matches exactly, including whitespace and line breaks"
	if hint := diagnoseMismatch(content, old); hint != "" {
		msg += "\n\n" + hint
	}
	return errors.New(msg)
}

// commitFileChange writes newContent to filePath, updates the file history,
// and records the read in the file tracker. Callers must convert line endings
// before calling this function.
func commitFileChange(edit editContext, sessionID, filePath, oldContent, newContent string) error {
	if err := os.WriteFile(filePath, []byte(newContent), 0o644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	file, err := edit.files.GetByPathAndSession(edit.ctx, filePath, sessionID)
	if err != nil {
		_, err = edit.files.Create(edit.ctx, sessionID, filePath, oldContent)
		if err != nil {
			return fmt.Errorf("error creating file history: %w", err)
		}
	}
	if file.Content != oldContent {
		// User manually changed the content; store an intermediate version.
		if _, err := edit.files.CreateVersion(edit.ctx, sessionID, filePath, oldContent); err != nil {
			slog.Error("Error creating file history version", "error", err)
		}
	}
	if _, err := edit.files.CreateVersion(edit.ctx, sessionID, filePath, newContent); err != nil {
		slog.Error("Error creating file history version", "error", err)
	}

	edit.filetracker.RecordRead(edit.ctx, sessionID, filePath)
	return nil
}

// loadExistingFile reads a file for an in-place edit. ok is false when
// the caller should return result immediately instead of proceeding; the
// other return values are only meaningful when ok is true.
func loadExistingFile(edit editContext, filePath, sessionError string) (sessionID, oldContent string, isCrlf bool, result Result, ok bool) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", "", false, Fail(fmt.Sprintf("file not found: %s", filePath)), false
		}
		return "", "", false, FailErr("failed to access file", err), false
	}

	if fileInfo.IsDir() {
		return "", "", false, Fail(fmt.Sprintf("path is a directory, not a file: %s", filePath)), false
	}

	sessionID = GetSessionFromContext(edit.ctx)
	if sessionID == "" {
		return "", "", false, Fail(sessionError), false
	}

	lastRead := edit.filetracker.LastReadTime(edit.ctx, sessionID, filePath)
	if lastRead.IsZero() {
		return "", "", false, Fail("you must read the file before editing it. Use the Read tool first"), false
	}

	modTime := fileInfo.ModTime().Truncate(time.Second)
	if modTime.After(lastRead) {
		return "", "", false, Fail(
			fmt.Sprintf(
				"file %s has been modified since it was last read (mod time: %s, last read: %s)",
				filePath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339),
			),
		), false
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", "", false, FailErr("failed to read file", err), false
	}

	oldContent, isCrlf = fsext.ToUnixLineEndings(string(content))
	return sessionID, oldContent, isCrlf, Result{}, true
}
