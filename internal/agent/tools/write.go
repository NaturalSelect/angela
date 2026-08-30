package tools

import (
	"context"
	_ "embed"
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
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/toolnames"

	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/permission"
)

//go:embed write.md
var writeDescription string

type WriteParams struct {
	FilePath string `json:"file_path" description:"The path to the file to write"`
	Content  string `json:"content" description:"The content to write to the file"`
}

type WritePermissionsParams struct {
	FilePath   string `json:"file_path"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

type WriteResponseMetadata struct {
	Diff      string `json:"diff"`
	Additions int    `json:"additions"`
	Removals  int    `json:"removals"`
}

type writeTool struct {
	fantasy.AgentTool

	lspManager  *lsp.Manager
	files       history.Service
	filetracker filetracker.Service
	workingDir  string
}

func NewWriteTool(
	lspManager *lsp.Manager,
	files history.Service,
	filetracker filetracker.Service,
	workingDir string,
) fantasy.AgentTool {
	t := &writeTool{
		lspManager:  lspManager,
		files:       files,
		filetracker: filetracker,
		workingDir:  workingDir,
	}
	t.AgentTool = fantasy.NewAgentTool(toolnames.Write, writeDescription, t.run)
	return t
}

func (t *writeTool) run(ctx context.Context, params WriteParams, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	plan, err := t.plan(ctx, params)
	if err != nil {
		return fantasy.ToolResponse{}, err
	}
	if plan.Response != nil {
		return *plan.Response, nil
	}
	return plan.Apply(ctx)
}

func (t *writeTool) Plan(ctx context.Context, call fantasy.ToolCall) (Plan, error) {
	params, ok := decodeInput[WriteParams](call.Input)
	if !ok {
		return Plan{}, fmt.Errorf("invalid input for %s", toolnames.Write)
	}
	return t.plan(ctx, params)
}

// plan reads what the write would replace and works out the diff,
// without creating anything. Nothing here touches the file being
// written, so a refused write leaves the disk as it found it.
func (t *writeTool) plan(ctx context.Context, params WriteParams) (Plan, error) {
	if params.FilePath == "" {
		return settled(fantasy.NewTextErrorResponse("file_path is required")), nil
	}

	sessionID := GetSessionFromContext(ctx)
	if sessionID == "" {
		return Plan{}, fmt.Errorf("session_id is required")
	}

	filePath := filepathext.SmartJoin(t.workingDir, params.FilePath)

	oldContent := ""
	fileInfo, err := os.Stat(filePath)
	switch {
	case err == nil:
		if fileInfo.IsDir() {
			return settled(fantasy.NewTextErrorResponse(fmt.Sprintf("Path is a directory, not a file: %s", filePath))), nil
		}

		modTime := fileInfo.ModTime().Truncate(time.Second)
		lastRead := t.filetracker.LastReadTime(ctx, sessionID, filePath)
		if modTime.After(lastRead) {
			return settled(fantasy.NewTextErrorResponse(fmt.Sprintf("File %s has been modified since it was last read.\nLast modification: %s\nLast read: %s\n\nPlease read the file again before modifying it.",
				filePath, modTime.Format(time.RFC3339), lastRead.Format(time.RFC3339)))), nil
		}

		if oldBytes, readErr := os.ReadFile(filePath); readErr == nil {
			oldContent = string(oldBytes)
			if oldContent == params.Content {
				return settled(fantasy.NewTextErrorResponse(fmt.Sprintf("File %s already contains the exact content. No changes made.", filePath))), nil
			}
		}
	case !os.IsNotExist(err):
		return Plan{}, fmt.Errorf("error checking file: %w", err)
	}

	unified, additions, removals := diff.GenerateDiff(
		oldContent,
		params.Content,
		strings.TrimPrefix(filePath, t.workingDir),
	)
	metadata := WriteResponseMetadata{
		Diff:      unified,
		Additions: additions,
		Removals:  removals,
	}

	return Plan{
		Preview: permission.Preview{
			Description: fmt.Sprintf("Create file %s", filePath),
			Params: WritePermissionsParams{
				FilePath:   filePath,
				OldContent: oldContent,
				NewContent: params.Content,
			},
		},
		Refusal: metadata,
		Apply: func(ctx context.Context) (fantasy.ToolResponse, error) {
			return t.apply(ctx, params, filePath, sessionID, oldContent, metadata)
		},
	}, nil
}

func (t *writeTool) apply(
	ctx context.Context,
	params WriteParams,
	filePath, sessionID, oldContent string,
	metadata WriteResponseMetadata,
) (fantasy.ToolResponse, error) {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("error creating directory: %w", err)
	}

	if err := os.WriteFile(filePath, []byte(params.Content), 0o644); err != nil {
		return fantasy.ToolResponse{}, fmt.Errorf("error writing file: %w", err)
	}

	file, err := t.files.GetByPathAndSession(ctx, filePath, sessionID)
	if err != nil {
		if _, err := t.files.Create(ctx, sessionID, filePath, oldContent); err != nil {
			return fantasy.ToolResponse{}, fmt.Errorf("error creating file history: %w", err)
		}
	}
	if file.Content != oldContent {
		// The user changed the file behind us; keep what they had.
		if _, err := t.files.CreateVersion(ctx, sessionID, filePath, oldContent); err != nil {
			slog.Error("Error creating file history version", "error", err)
		}
	}
	if _, err := t.files.CreateVersion(ctx, sessionID, filePath, params.Content); err != nil {
		slog.Error("Error creating file history version", "error", err)
	}

	t.filetracker.RecordRead(ctx, sessionID, filePath)

	notifyLSPs(ctx, t.lspManager, params.FilePath)

	result := fmt.Sprintf("File successfully written: %s", filePath)
	result = fmt.Sprintf("<result>\n%s\n</result>", result)
	result += getDiagnostics(filePath, t.lspManager)
	return fantasy.WithResponseMetadata(fantasy.NewTextResponse(result), metadata), nil
}
