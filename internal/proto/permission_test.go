package proto_test

import (
	"encoding/json"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

// TestPermissionRequestParamsTypeAssertable guards the permission
// dialog's type assertions across the client/server boundary. The TUI
// asserts PermissionRequest.Params to tools.*PermissionsParams; when
// the request round-trips over the SSE wire (server → client), the
// decoded value must be the same Go type, otherwise the dialog
// renders empty content.
func TestPermissionRequestParamsTypeAssertable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolName string
		params   any
		assert   func(t *testing.T, got any)
	}{
		{
			name:     "bash",
			toolName: toolnames.Bash,
			params: tools.BashPermissionsParams{
				Description:     "list files",
				Command:         "ls -la",
				WorkingDir:      "/tmp",
				RunInBackground: false,
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.BashPermissionsParams)
				require.True(t, ok, "params must decode as tools.BashPermissionsParams, got %T", got)
				require.Equal(t, "list files", v.Description)
				require.Equal(t, "ls -la", v.Command)
				require.Equal(t, "/tmp", v.WorkingDir)
			},
		},
		{
			name:     "edit",
			toolName: toolnames.Edit,
			params: tools.EditPermissionsParams{
				FilePath:   "/tmp/x.go",
				OldContent: "old",
				NewContent: "new",
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.EditPermissionsParams)
				require.True(t, ok, "params must decode as tools.EditPermissionsParams, got %T", got)
				require.Equal(t, "/tmp/x.go", v.FilePath)
				require.Equal(t, "old", v.OldContent)
				require.Equal(t, "new", v.NewContent)
			},
		},
		{
			name:     "write",
			toolName: toolnames.Write,
			params: tools.WritePermissionsParams{
				FilePath:   "/tmp/x.go",
				NewContent: "new",
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.WritePermissionsParams)
				require.True(t, ok, "params must decode as tools.WritePermissionsParams, got %T", got)
				require.Equal(t, "/tmp/x.go", v.FilePath)
				require.Equal(t, "new", v.NewContent)
			},
		},
		{
			name:     "multiedit",
			toolName: toolnames.MultiEdit,
			params: tools.MultiEditPermissionsParams{
				FilePath:   "/tmp/x.go",
				OldContent: "old",
				NewContent: "new",
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.MultiEditPermissionsParams)
				require.True(t, ok, "params must decode as tools.MultiEditPermissionsParams, got %T", got)
				require.Equal(t, "/tmp/x.go", v.FilePath)
			},
		},
		{
			name:     "ls",
			toolName: toolnames.LS,
			params: tools.LSPermissionsParams{
				Path:   "/tmp",
				Ignore: []string{".git"},
				Depth:  2,
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.LSPermissionsParams)
				require.True(t, ok, "params must decode as tools.LSPermissionsParams, got %T", got)
				require.Equal(t, "/tmp", v.Path)
				require.Equal(t, []string{".git"}, v.Ignore)
				require.Equal(t, 2, v.Depth)
			},
		},
		{
			name:     "view",
			toolName: toolnames.View,
			params: tools.ViewPermissionsParams{
				FilePath: "/tmp/x.go",
				Offset:   10,
				Limit:    100,
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.ViewPermissionsParams)
				require.True(t, ok, "params must decode as tools.ViewPermissionsParams, got %T", got)
				require.Equal(t, "/tmp/x.go", v.FilePath)
			},
		},
		{
			name:     "fetch",
			toolName: toolnames.Fetch,
			params: tools.FetchPermissionsParams{
				URL:    "https://example.com",
				Format: "text",
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.FetchPermissionsParams)
				require.True(t, ok, "params must decode as tools.FetchPermissionsParams, got %T", got)
				require.Equal(t, "https://example.com", v.URL)
			},
		},
		{
			name:     "download",
			toolName: toolnames.Download,
			params: tools.DownloadPermissionsParams{
				URL:      "https://example.com/x.zip",
				FilePath: "/tmp/x.zip",
				Timeout:  30,
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.DownloadPermissionsParams)
				require.True(t, ok, "params must decode as tools.DownloadPermissionsParams, got %T", got)
				require.Equal(t, "https://example.com/x.zip", v.URL)
				require.Equal(t, "/tmp/x.zip", v.FilePath)
			},
		},
		{
			name:     "web_fetch",
			toolName: toolnames.WebFetch,
			params: tools.WebFetchPermissionsParams{
				URL: "https://example.com",
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.WebFetchPermissionsParams)
				require.True(t, ok, "params must decode as tools.WebFetchPermissionsParams, got %T", got)
				require.Equal(t, "https://example.com", v.URL)
			},
		},
		{
			name:     "web_search",
			toolName: toolnames.WebSearch,
			params: tools.WebSearchPermissionsParams{
				Query:      "web fetch permissions",
				MaxResults: 5,
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.WebSearchPermissionsParams)
				require.True(t, ok, "params must decode as tools.WebSearchPermissionsParams, got %T", got)
				require.Equal(t, "web fetch permissions", v.Query)
				require.Equal(t, 5, v.MaxResults)
			},
		},
		{
			name:     "lsp_rename",
			toolName: toolnames.LSPRename,
			params: tools.RenamePermissionsParams{
				Symbol:  "OldName",
				NewName: "NewName",
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.RenamePermissionsParams)
				require.True(t, ok, "params must decode as tools.RenamePermissionsParams, got %T", got)
				require.Equal(t, "OldName", v.Symbol)
				require.Equal(t, "NewName", v.NewName)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Build a server-side request with the tool's concrete
			// params type, marshal to JSON (the wire path), then
			// decode back through proto.PermissionRequest.
			outbound := proto.PermissionRequest{
				ID:         "perm-1",
				SessionID:  "sess-1",
				ToolCallID: "call-1",
				ToolName:   tc.toolName,
				Path:       "/tmp",
				Params:     tc.params,
			}
			data, err := json.Marshal(outbound)
			require.NoError(t, err)

			var inbound proto.PermissionRequest
			require.NoError(t, json.Unmarshal(data, &inbound))

			tc.assert(t, inbound.Params)
		})
	}
}
