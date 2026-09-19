package tools

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed web_fetch.md.tpl
var webFetchDescriptionTmpl []byte

var webFetchDescriptionTpl = template.Must(
	template.New("webFetchDescription").
		Parse(string(webFetchDescriptionTmpl)),
)

// WebFetchScratchDir returns the scratch subdirectory for one
// session's cached pages under root, or an error if sessionID is not
// safe to use as a single path component.
//
// For delegated agents, sessionID is built from a provider-supplied
// tool-call ID (coordinator.CreateAgentToolSessionID), which this
// package cannot trust: filepath.Join normalizes rather than sandboxes,
// so a value containing a path separator or a "." / ".." segment would
// otherwise let a malicious provider response point the join outside
// root. Both the tool that creates this directory and the coordinator
// that later removes it call this, so neither can disagree with the
// other about what counts as safe.
func WebFetchScratchDir(root, sessionID string) (string, error) {
	if sessionID == "" || sessionID == "." || sessionID == ".." || strings.ContainsAny(sessionID, `/\`) {
		return "", fmt.Errorf("unsafe session id %q for web_fetch scratch directory", sessionID)
	}
	return filepath.Join(root, sessionID), nil
}

// NewWebFetchTool creates a web fetch tool for sub-agents. scratchDir is
// the root where large pages get saved for grep/read; each session gets
// its own subdirectory under it, created on first use, so cleanup can
// discard one session's pages without touching a concurrent session's.
func NewWebFetchTool(scratchDir string, client *http.Client) fantasy.AgentTool {
	if client == nil {
		client = newDefaultHTTPClient(defaultToolHTTPTimeout)
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
				sessionScratchDir, err := WebFetchScratchDir(scratchDir, sessionID)
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
