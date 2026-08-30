// Package toolnames holds the canonical wire names of Angela's built-in
// tools.
//
// These names are protocol values: the model sees them in tool calls and
// users write them in angela.json allow-lists, so the strings must never
// change once shipped.
//
// The package exists because the names are needed by packages that cannot
// import internal/agent/tools: that package already depends on
// internal/config and internal/permission, so either of them importing it
// back would be an import cycle. Keeping this package dependency-free lets
// every consumer import it instead of re-declaring literals.
package toolnames

// Core file and shell tools.
const (
	Bash      = "bash"
	Edit      = "edit"
	MultiEdit = "multiedit"
	View      = "view"
	Write     = "write"
	Glob      = "glob"
	Grep      = "grep"
	LS        = "ls"
)

// Network tools.
const (
	Download    = "download"
	Fetch       = "fetch"
	WebFetch    = "web_fetch"
	WebSearch   = "web_search"
	Sourcegraph = "sourcegraph"
)

// LSP tools.
const (
	LSPDiagnostics   = "lsp_diagnostics"
	LSPReferences    = "lsp_references"
	LSPRestart       = "lsp_restart"
	LSPSymbols       = "lsp_symbols"
	LSPDefinition    = "lsp_definition"
	LSPCallHierarchy = "lsp_call_hierarchy"
	LSPRename        = "lsp_rename"
	LSPReplaceSymbol = "lsp_replace_symbol"
)

// Background job tools.
const (
	JobOutput = "job_output"
	JobKill   = "job_kill"
)

// MCP tools.
const (
	ListMCPResources = "list_mcp_resources"
	ReadMCPResource  = "read_mcp_resource"
)

// Session and introspection tools.
const (
	Agent      = "agent"
	AngelaInfo = "angela_info"
	AngelaLogs = "angela_logs"
	LoadReport = "load_report"
	Question   = "question"
	Todos      = "todos"
)

// Branch-mode tools. These are not part of the default tool set; the
// coordinator appends them for agents running in branch mode.
const (
	Merge         = "merge"
	ProposalWrite = "proposal_write"
	ProposalEdit  = "proposal_edit"
	ProposalRead  = "proposal_read"
)

// MCPPrefix prefixes the dynamically generated name of every MCP tool,
// which takes the form mcp_<server>_<tool>.
const MCPPrefix = "mcp_"
