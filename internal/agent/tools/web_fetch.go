package tools

import (
	"context"
	_ "embed"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"text/template"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/browserhttp"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed web_fetch.md.tpl
var webFetchDescriptionTmpl []byte

var webFetchDescriptionTpl = template.Must(
	template.New("webFetchDescription").
		Parse(string(webFetchDescriptionTmpl)),
)

// NewWebFetchTool creates a web fetch tool. scratchDir is the root where
// large pages get saved for grep/read; each session gets its own
// subdirectory under it, created on first use, so cleanup can discard one
// session's pages without touching a concurrent session's. Session
// directories left untouched for a day are pruned on the next large save.
func NewWebFetchTool(scratchDir string, client *http.Client) fantasy.AgentTool {
	if client == nil {
		client = browserhttp.NewClient(browserhttp.Options{})
	}

	return NewParallelTool(
		toolnames.WebFetch,
		renderToolDescription(webFetchDescriptionTpl),
		func(ctx context.Context, params WebFetchParams, call fantasy.ToolCall) Result {
			if params.URL == "" {
				return Fail("url is required")
			}

			sessionID := GetSessionFromContext(ctx)
			if sessionID == "" {
				return Fail("session ID is required for creating a new file")
			}

			content, err := FetchURLAndConvert(ctx, client, params.URL)
			if err != nil {
				return Failf("Failed to fetch URL: %s", err)
			}

			hasLargeContent := len(content) > LargeContentThreshold
			var result strings.Builder

			if hasLargeContent {
				if err := PruneSessionScratch(scratchDir, SessionScratchRetention); err != nil {
					slog.Warn("Failed to prune old web_fetch pages", "error", err)
				}
				sessionScratchDir, err := SessionScratchDir(scratchDir, sessionID)
				if err != nil {
					return Fail(err.Error())
				}
				if err := os.MkdirAll(sessionScratchDir, 0o700); err != nil {
					return Failf("Failed to create scratch directory: %s", err)
				}

				tempFile, err := os.CreateTemp(sessionScratchDir, "page-*.md")
				if err != nil {
					return Failf("Failed to create temporary file: %s", err)
				}
				tempFilePath := tempFile.Name()

				if _, err := tempFile.WriteString(content); err != nil {
					_ = tempFile.Close() // Best effort close
					return Failf("Failed to write content to file: %s", err)
				}
				if err := tempFile.Close(); err != nil {
					return Failf("Failed to close temporary file: %s", err)
				}

				fmt.Fprintf(&result, "Fetched content from %s (large page)\n\n", params.URL)
				fmt.Fprintf(&result, "Content saved to: %s\n\n", tempFilePath)
				result.WriteString("Use the read and grep tools to analyze this file.")
			} else {
				fmt.Fprintf(&result, "Fetched content from %s:\n\n", params.URL)
				result.WriteString(content)
			}

			return Ok(result.String())
		},
	)
}
