package tools

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/filepathext"
	"github.com/NaturalSelect/angela/internal/permission"
)

// agentToolName mirrors agent.AgentToolName. It is duplicated because
// package agent depends on this one, so the constant cannot be imported.
const agentToolName = "agent"

// MergeToolName names the merge tool, whose implementation lives in
// package agent because it needs the branch controller. The name is
// declared here so the permission system and the approval dialog can
// reach it from this side of the dependency.
const MergeToolName = "merge"

// MergePermissionsParams is what the approval dialog renders for a
// merge. The proposal being handed back is shown as a diff, so it
// carries both sides like the file tools' equivalents do.
type MergePermissionsParams struct {
	Name       string `json:"name"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

// mcpToolPrefix starts the generated name of every dynamic MCP tool,
// which is built as mcp_<server>_<tool>.
const mcpToolPrefix = "mcp_"

// MCPIdentity is implemented by the dynamic MCP tools, which carry the
// server and the tool they call.
//
// Their generated name cannot be taken apart again: both halves may
// hold underscores, so mcp_prod_admin_delete_user reads equally well as
// prod/admin_delete_user and prod_admin/delete_user. Guessing decides
// which rules apply, so the identity has to come from the tool.
type MCPIdentity interface {
	MCP() string
	MCPToolName() string
}

// AccessOfTool maps a tool call to the access it requests, with the
// tool itself in hand. Prefer it over AccessOf: it is the only form
// that can describe a dynamic MCP call correctly.
func AccessOfTool(tool fantasy.AgentTool, toolName, rawInput, workingDir string) (permission.Access, bool) {
	if id, ok := tool.(MCPIdentity); ok {
		return permission.Access{
			Tool:    toolName,
			Action:  permission.ActionMCP,
			Server:  id.MCP(),
			MCPTool: id.MCPToolName(),
		}, true
	}
	return AccessOf(toolName, rawInput, workingDir)
}

// PreviewOfTool describes a call for the approval dialog, naming a
// dynamic MCP tool by the identity it reports rather than by a guess
// made from its name.
func PreviewOfTool(tool fantasy.AgentTool, toolName, rawInput, workingDir string) permission.Preview {
	if id, ok := tool.(MCPIdentity); ok {
		return permission.Preview{
			Description: fmt.Sprintf(
				"execute %s/%s with the following parameters:", id.MCP(), id.MCPToolName()),
			Params: rawInput,
		}
	}
	return PreviewOf(toolName, rawInput, workingDir)
}

// AccessOf maps a tool call to the access it requests. It is the only
// place in the permission system that knows about individual tools:
// everything downstream decides on the returned Access alone.
//
// It reports false for a tool it does not know and for input it cannot
// decode, which callers must treat as fail-closed. Adding a tool
// without adding it here therefore denies the tool rather than
// silently exempting it from permission checks.
//
// A dynamic MCP tool is among the ones it cannot describe, because only
// the tool knows which underscore in its name separates the server from
// the tool. Callers holding the tool use AccessOfTool instead.
func AccessOf(toolName, rawInput, workingDir string) (permission.Access, bool) {
	access := permission.Access{Tool: toolName}

	resolve := func(path string) string {
		switch {
		case path == "" || path == ".":
			return workingDir
		// SmartIsAbs, not filepath.IsAbs: the tools reach their files
		// through filepathext.SmartJoin, which on Windows treats a
		// leading slash as absolute even without a volume. Judging
		// such a path as workspace-relative would describe a file the
		// tool is not going to touch.
		case filepathext.SmartIsAbs(path):
			return filepath.Clean(path)
		default:
			return filepath.Join(workingDir, path)
		}
	}

	if strings.HasPrefix(toolName, mcpToolPrefix) {
		return access, false
	}

	switch toolName {
	case BashToolName:
		p, ok := decodeInput[BashParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionExecute
		access.Command = p.Command
		access.Path = resolve(p.WorkingDir)

	case EditToolName:
		p, ok := decodeInput[EditParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionEdit
		access.Path = resolve(p.FilePath)

	case MultiEditToolName:
		p, ok := decodeInput[MultiEditParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionEdit
		access.Path = resolve(p.FilePath)

	case WriteToolName:
		p, ok := decodeInput[WriteParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionEdit
		access.Path = resolve(p.FilePath)

	case ReplaceSymbolToolName:
		p, ok := decodeInput[ReplaceSymbolParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionEdit
		access.Path = resolve(p.FilePath)

	case RenameToolName:
		p, ok := decodeInput[RenameParams](rawInput)
		if !ok {
			return access, false
		}
		// A rename rewrites every file holding a reference, so the
		// access covers the search root rather than one file.
		access.Action = permission.ActionEdit
		access.Path = resolve(p.Path)

	case ViewToolName:
		p, ok := decodeInput[ViewParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionRead
		access.Path = resolve(p.FilePath)

	case LSToolName:
		p, ok := decodeInput[LSParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionList
		access.Path = resolve(p.Path)

	case GlobToolName:
		p, ok := decodeInput[GlobParams](rawInput)
		if !ok {
			return access, false
		}
		// The literal head of the pattern is part of the search root,
		// so a pattern climbing out with ".." stays visible.
		prefix, _ := filepathext.SplitGlobPrefix(p.Pattern)
		access.Action = permission.ActionList
		access.Path = resolve(filepath.Join(p.Path, prefix))

	case GrepToolName:
		p, ok := decodeInput[GrepParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionRead
		access.Path = resolve(p.Path)

	case DiagnosticsToolName:
		p, ok := decodeInput[DiagnosticsParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionRead
		access.Path = resolve(p.FilePath)

	case SymbolsToolName:
		p, ok := decodeInput[SymbolsParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionRead
		access.Path = resolve(p.FilePath)

	case ReferencesToolName:
		p, ok := decodeInput[ReferencesParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionRead
		access.Path = resolve(p.Path)

	case DefinitionToolName:
		p, ok := decodeInput[DefinitionParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionRead
		access.Path = resolve(p.Path)

	case CallHierarchyToolName:
		p, ok := decodeInput[CallHierarchyParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionRead
		access.Path = resolve(p.Path)

	case DownloadToolName:
		p, ok := decodeInput[DownloadParams](rawInput)
		if !ok {
			return access, false
		}
		// A download both fetches and writes, so it carries the file it
		// lands in alongside the URL.
		access.Action = permission.ActionNetwork
		access.URL = p.URL
		access.Path = resolve(p.FilePath)

	case FetchToolName:
		p, ok := decodeInput[FetchParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionNetwork
		access.URL = p.URL

	case WebFetchToolName:
		p, ok := decodeInput[WebFetchParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionNetwork
		access.URL = p.URL

	case WebSearchToolName:
		p, ok := decodeInput[WebSearchParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionNetwork
		access.URL = p.Query

	case SourcegraphToolName:
		p, ok := decodeInput[SourcegraphParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionNetwork
		access.URL = p.Query

	case ListMCPResourcesToolName:
		p, ok := decodeInput[ListMCPResourcesParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionMCP
		access.Server = p.MCPName

	case ReadMCPResourceToolName:
		p, ok := decodeInput[ReadMCPResourceParams](rawInput)
		if !ok {
			return access, false
		}
		access.Action = permission.ActionMCP
		access.Server = p.MCPName
		access.MCPTool = p.URI

	case JobKillToolName:
		// Killing a job only reaches a shell this agent started under
		// an already approved bash call, so the gate sat on that call.
		access.Action = permission.ActionRead

	case LSPRestartToolName:
		// Restarts a server the user configured; it starts nothing new.
		access.Action = permission.ActionRead

	case MergeToolName:
		// Reaches no file and runs nothing, but it is the one moment
		// the user decides whether this branch's result crosses back
		// and the branch ends. Never in scope: the gate always asks.
		access.Action = permission.ActionMerge

	case ProposalWriteToolName, ProposalEditToolName, ProposalReadToolName:
		// The proposal is the branch's own draft, held in memory and
		// never written anywhere. The gate that matters sits on merge,
		// where the user decides whether the finished text crosses
		// back; stopping to approve each revision on the way there
		// would ask about a document that reaches nothing.
		access.Action = permission.ActionRead

	case agentToolName, TodosToolName, QuestionToolName, AngelaInfoToolName,
		AngelaLogsToolName, JobOutputToolName:
		// These read state the agent already has. The real gate sits on
		// the calls a subagent goes on to make.
		access.Action = permission.ActionRead

	default:
		return access, false
	}

	return access, true
}

// PreviewOf builds what the permission dialog shows for a tool call.
// It sits next to AccessOf because both answer a per-tool question the
// rest of the permission system must not have to ask: AccessOf says
// what a call reaches, PreviewOf says how to show it.
//
// Tools that render their own preview are absent here: they compute a
// diff from the file they are about to rewrite, which cannot be
// derived from the arguments alone. They get a ticket instead.
func PreviewOf(toolName, rawInput, workingDir string) permission.Preview {
	switch toolName {
	case BashToolName:
		if p, ok := decodeInput[BashParams](rawInput); ok {
			return permission.Preview{
				Description: "Execute command: " + p.Command,
				Params:      BashPermissionsParams(p),
			}
		}

	case ViewToolName:
		if p, ok := decodeInput[ViewParams](rawInput); ok {
			return permission.Preview{
				Description: "Read file outside working directory: " + p.FilePath,
				Params:      ViewPermissionsParams(p),
			}
		}

	case LSToolName:
		if p, ok := decodeInput[LSParams](rawInput); ok {
			return permission.Preview{
				Description: "List directory outside working directory: " + p.Path,
				Params:      LSPermissionsParams(p),
			}
		}

	case DownloadToolName:
		if p, ok := decodeInput[DownloadParams](rawInput); ok {
			return permission.Preview{
				Description: fmt.Sprintf("Download file from URL: %s to %s", p.URL, p.FilePath),
				Params:      DownloadPermissionsParams(p),
			}
		}

	case FetchToolName:
		if p, ok := decodeInput[FetchParams](rawInput); ok {
			return permission.Preview{
				Description: "Fetch content from URL: " + p.URL,
				Params:      FetchPermissionsParams(p),
			}
		}

	case WebFetchToolName:
		if p, ok := decodeInput[WebFetchParams](rawInput); ok {
			return permission.Preview{
				Description: "Fetch content from URL: " + p.URL,
				Params:      WebFetchPermissionsParams(p),
			}
		}

	case WebSearchToolName:
		if p, ok := decodeInput[WebSearchParams](rawInput); ok {
			return permission.Preview{
				Description: "Search the web for: " + p.Query,
				Params:      WebSearchPermissionsParams(p),
			}
		}

	case ListMCPResourcesToolName:
		if p, ok := decodeInput[ListMCPResourcesParams](rawInput); ok {
			return permission.Preview{
				Description: "List MCP resources from " + p.MCPName,
				Params:      ListMCPResourcesPermissionsParams(p),
			}
		}

	case ReadMCPResourceToolName:
		if p, ok := decodeInput[ReadMCPResourceParams](rawInput); ok {
			return permission.Preview{
				Description: "Read MCP resource from " + p.MCPName,
				Params:      ReadMCPResourcePermissionsParams(p),
			}
		}
	}

	return permission.Preview{}
}

func decodeInput[T any](raw string) (T, bool) {
	var params T
	if strings.TrimSpace(raw) == "" {
		return params, true
	}
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		var zero T
		return zero, false
	}
	return params, true
}
