package proto_test

import (
	"encoding/json"
	"fmt"
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

// TestPermissionRequestAdditionalToolParams covers the unmarshalToolParams
// branches not exercised by TestPermissionRequestParamsTypeAssertable.
func TestPermissionRequestAdditionalToolParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		toolName string
		params   any
		assert   func(t *testing.T, got any)
	}{
		{
			name:     "lsp_replace_symbol",
			toolName: toolnames.LSPReplaceSymbol,
			params: tools.ReplaceSymbolPermissionsParams{
				FilePath:   "/tmp/x.go",
				OldContent: "old",
				NewContent: "new",
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.ReplaceSymbolPermissionsParams)
				require.True(t, ok, "params must decode as tools.ReplaceSymbolPermissionsParams, got %T", got)
				require.Equal(t, "/tmp/x.go", v.FilePath)
				require.Equal(t, "old", v.OldContent)
				require.Equal(t, "new", v.NewContent)
			},
		},
		{
			name:     "list_mcp_resources",
			toolName: toolnames.ListMCPResources,
			params: tools.ListMCPResourcesPermissionsParams{
				MCPName: "my-server",
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.ListMCPResourcesPermissionsParams)
				require.True(t, ok, "params must decode as tools.ListMCPResourcesPermissionsParams, got %T", got)
				require.Equal(t, "my-server", v.MCPName)
			},
		},
		{
			name:     "read_mcp_resource",
			toolName: toolnames.ReadMCPResource,
			params: tools.ReadMCPResourcePermissionsParams{
				MCPName: "my-server",
				URI:     "file:///readme.md",
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.ReadMCPResourcePermissionsParams)
				require.True(t, ok, "params must decode as tools.ReadMCPResourcePermissionsParams, got %T", got)
				require.Equal(t, "my-server", v.MCPName)
				require.Equal(t, "file:///readme.md", v.URI)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			outbound := proto.PermissionRequest{
				ID:         "perm-2",
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

// TestUnmarshalToolParamsUnknownToolFallsBackToGenericMap covers the
// default branch of unmarshalToolParams, used for tools angela has no
// typed permission schema for (e.g. dynamically registered MCP tools).
func TestUnmarshalToolParamsUnknownToolFallsBackToGenericMap(t *testing.T) {
	t.Parallel()

	outbound := proto.PermissionRequest{
		ID:       "perm-3",
		ToolName: "MCP_custom_server_do_thing",
		Params:   map[string]any{"foo": "bar", "count": float64(5)},
	}
	data, err := json.Marshal(outbound)
	require.NoError(t, err)

	var inbound proto.PermissionRequest
	require.NoError(t, json.Unmarshal(data, &inbound))

	require.Equal(t, map[string]any{"foo": "bar", "count": float64(5)}, inbound.Params)
}

// TestCreatePermissionRequestParamsTypeAssertable mirrors
// TestPermissionRequestParamsTypeAssertable for CreatePermissionRequest,
// whose UnmarshalJSON independently drives the same unmarshalToolParams
// helper.
func TestCreatePermissionRequestParamsTypeAssertable(t *testing.T) {
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
				Description: "list files",
				Command:     "ls -la",
			},
			assert: func(t *testing.T, got any) {
				v, ok := got.(tools.BashPermissionsParams)
				require.True(t, ok, "params must decode as tools.BashPermissionsParams, got %T", got)
				require.Equal(t, "ls -la", v.Command)
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
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			outbound := proto.CreatePermissionRequest{
				SessionID:   "sess-1",
				ToolCallID:  "call-1",
				ToolName:    tc.toolName,
				Description: "desc",
				Action:      "allow",
				Params:      tc.params,
				Path:        "/tmp",
			}
			data, err := json.Marshal(outbound)
			require.NoError(t, err)

			var inbound proto.CreatePermissionRequest
			require.NoError(t, json.Unmarshal(data, &inbound))

			tc.assert(t, inbound.Params)
		})
	}
}

func TestPermissionRequestUnmarshalJSONMalformed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
	}{
		{"invalid json syntax", `{`},
		// Valid top-level JSON syntax so the outer json.Unmarshal actually
		// dispatches into PermissionRequest.UnmarshalJSON, where the field
		// type mismatch (id expects a string) fails the aux decode step
		// before params are even considered.
		{"field type mismatch outside params", `{"id":123,"tool_name":"Bash"}`},
		{"known tool param type mismatch", `{"tool_name":"Bash","params":{"command":123}}`},
		{"unknown tool generic map type mismatch", `{"tool_name":"SomeUnknownTool","params":"not-an-object"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got proto.PermissionRequest
			err := json.Unmarshal([]byte(tc.data), &got)
			require.Error(t, err)
		})
	}
}

func TestCreatePermissionRequestUnmarshalJSONMalformed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
	}{
		{"invalid json syntax", `{`},
		{"field type mismatch outside params", `{"session_id":123,"tool_name":"Bash"}`},
		{"known tool param type mismatch", `{"tool_name":"Bash","params":{"command":123}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got proto.CreatePermissionRequest
			err := json.Unmarshal([]byte(tc.data), &got)
			require.Error(t, err)
		})
	}
}

// TestUnmarshalToolParamsFieldTypeMismatch asserts that every known tool
// name's typed params decoding fails cleanly (not a panic) when the wire
// payload's field types don't match the tool's permission params schema,
// covering each case in unmarshalToolParams' switch.
func TestUnmarshalToolParamsFieldTypeMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		toolName    string
		badParamKey string
	}{
		{"bash", toolnames.Bash, "command"},
		{"download", toolnames.Download, "file_path"},
		{"edit", toolnames.Edit, "file_path"},
		{"write", toolnames.Write, "file_path"},
		{"multiedit", toolnames.MultiEdit, "file_path"},
		{"fetch", toolnames.Fetch, "url"},
		{"web_fetch", toolnames.WebFetch, "url"},
		{"web_search", toolnames.WebSearch, "query"},
		{"view", toolnames.View, "file_path"},
		{"ls", toolnames.LS, "path"},
		{"lsp_replace_symbol", toolnames.LSPReplaceSymbol, "file_path"},
		{"list_mcp_resources", toolnames.ListMCPResources, "mcp_name"},
		{"read_mcp_resource", toolnames.ReadMCPResource, "mcp_name"},
		{"lsp_rename", toolnames.LSPRename, "symbol"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data := fmt.Sprintf(`{"tool_name":%q,"params":{%q:123}}`, tc.toolName, tc.badParamKey)
			var got proto.PermissionRequest
			err := json.Unmarshal([]byte(data), &got)
			require.Error(t, err)
		})
	}
}
