package tools

import (
	"context"
	"fmt"
	"log/slog"
	"mime"
	"os"
	"path/filepath"

	"charm.land/fantasy"

	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

// GetMCPTools gets all the currently available MCP tools.
func GetMCPTools(cfg *config.ConfigStore, wd string) []*Tool {
	var result []*Tool
	for mcpName, tools := range mcp.Tools() {
		for _, tool := range tools {
			result = append(result, &Tool{
				mcpName:    mcpName,
				tool:       tool,
				workingDir: wd,
				cfg:        cfg,
			})
		}
	}
	return result
}

// Tool is a tool from a MCP.
type Tool struct {
	mcpName         string
	tool            *mcp.Tool
	cfg             *config.ConfigStore
	workingDir      string
	providerOptions fantasy.ProviderOptions
}

func (m *Tool) SetProviderOptions(opts fantasy.ProviderOptions) {
	m.providerOptions = opts
}

func (m *Tool) ProviderOptions() fantasy.ProviderOptions {
	return m.providerOptions
}

func (m *Tool) Name() string {
	return toolnames.MCPPrefix + m.mcpName + "_" + m.tool.Name
}

func (m *Tool) MCP() string {
	return m.mcpName
}

func (m *Tool) MCPToolName() string {
	return m.tool.Name
}

func (m *Tool) Info() fantasy.ToolInfo {
	parameters := make(map[string]any)
	required := make([]string, 0)

	if input, ok := m.tool.InputSchema.(map[string]any); ok {
		if props, ok := input["properties"].(map[string]any); ok {
			parameters = props
		}
		if req, ok := input["required"].([]any); ok {
			// Convert []any -> []string when elements are strings
			for _, v := range req {
				if s, ok := v.(string); ok {
					required = append(required, s)
				}
			}
		} else if reqStr, ok := input["required"].([]string); ok {
			// Handle case where it's already []string
			required = reqStr
		}
	}

	return fantasy.ToolInfo{
		Name:        m.Name(),
		Description: m.tool.Description,
		Parameters:  parameters,
		Required:    required,
	}
}

func (m *Tool) Run(ctx context.Context, params fantasy.ToolCall) (fantasy.ToolResponse, error) {
	sessionID := GetSessionFromContext(ctx)
	if sessionID == "" {
		return fantasy.NewTextErrorResponse("session ID is required for creating a new file"), nil
	}

	result, err := mcp.RunTool(ctx, m.cfg, m.mcpName, m.tool.Name, params.Input)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}

	switch result.Type {
	case "image", "media":
		if !GetSupportsImagesFromContext(ctx) {
			modelName := GetModelNameFromContext(ctx)
			return fantasy.NewTextErrorResponse(fmt.Sprintf("This model (%s) does not support image data.", modelName)), nil
		}

		var response fantasy.ToolResponse
		if result.Type == "image" {
			response = fantasy.NewImageResponse(result.Data, result.MediaType)
		} else {
			response = fantasy.NewMediaResponse(result.Data, result.MediaType)
		}
		response.Content = result.Content
		if savedPath, err := saveMCPMedia(m.cfg.Config().Options.DataDirectory, result.Data, result.MediaType); err != nil {
			slog.Warn("Failed to save MCP media", "mcp", m.mcpName, "tool", m.tool.Name, "error", err)
		} else {
			response.Content = mediaSavedNote(result.Content, savedPath)
		}
		return response, nil
	default:
		return fantasy.NewTextResponse(result.Content), nil
	}
}

// mcpMediaDirName is the subdirectory of the data directory that holds
// image and media payloads returned by MCP tools.
const mcpMediaDirName = "mcp-media"

// saveMCPMedia writes data to a new file under the data directory and
// returns its path. The file name is generated locally so nothing the
// MCP server or the model supplied ends up in the path.
func saveMCPMedia(dataDirectory string, data []byte, mediaType string) (string, error) {
	dir := filepath.Join(dataDirectory, mcpMediaDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating media directory: %w", err)
	}

	f, err := os.CreateTemp(dir, "*"+mcpMediaExtension(mediaType))
	if err != nil {
		return "", fmt.Errorf("creating media file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("writing media file: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("closing media file: %w", err)
	}
	return f.Name(), nil
}

// mcpMediaExtension picks a predictable file extension for mediaType.
func mcpMediaExtension(mediaType string) string {
	base, _, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return ".bin"
	}
	switch base {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	if exts, err := mime.ExtensionsByType(base); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

// mediaSavedNote appends the saved file location to the tool's own text.
func mediaSavedNote(text, savedPath string) string {
	note := "The file is saved at " + savedPath + "."
	if text == "" {
		return note
	}
	return text + "\n\n" + note
}
