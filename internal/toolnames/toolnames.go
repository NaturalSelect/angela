// Package toolnames holds the canonical wire names of Angela's built-in
// tools.
//
// These names are protocol values: the model sees them in tool calls and
// users write them in angela.json allow-lists, so the strings must never
// change once shipped.
//
// Every name is the PascalCase spelling of its own Go identifier, which
// keeps the two impossible to drift apart and matches the convention the
// system prompts are written against.
//
// The package exists because the names are needed by packages that cannot
// import internal/agent/tools: that package already depends on
// internal/config and internal/permission, so either of them importing it
// back would be an import cycle. Keeping this package dependency-free lets
// every consumer import it instead of re-declaring literals.
package toolnames

// Core file and shell tools.
const (
	Bash      = "Bash"
	Edit      = "Edit"
	MultiEdit = "MultiEdit"
	View      = "View"
	Write     = "Write"
	Glob      = "Glob"
	Grep      = "Grep"
	LS        = "LS"
)

// Network tools.
const (
	Download    = "Download"
	Fetch       = "Fetch"
	WebFetch    = "WebFetch"
	WebSearch   = "WebSearch"
	Sourcegraph = "Sourcegraph"
)

// LSP tools.
const (
	LSPDiagnostics   = "LSPDiagnostics"
	LSPReferences    = "LSPReferences"
	LSPRestart       = "LSPRestart"
	LSPSymbols       = "LSPSymbols"
	LSPDefinition    = "LSPDefinition"
	LSPCallHierarchy = "LSPCallHierarchy"
	LSPRename        = "LSPRename"
	LSPReplaceSymbol = "LSPReplaceSymbol"
)

// Background job tools.
const (
	JobOutput = "JobOutput"
	JobKill   = "JobKill"
)

// MCP tools.
const (
	ListMCPResources = "ListMCPResources"
	ReadMCPResource  = "ReadMCPResource"
)

// Session and introspection tools.
const (
	Agent      = "Agent"
	AngelaInfo = "AngelaInfo"
	AngelaLogs = "AngelaLogs"
	LoadReport = "LoadReport"
	Question   = "Question"
	Todos      = "Todos"
)

// Branch-mode tools. These are not part of the default tool set; the
// coordinator appends them for agents running in branch mode.
const (
	Merge         = "Merge"
	ProposalWrite = "ProposalWrite"
	ProposalEdit  = "ProposalEdit"
	ProposalRead  = "ProposalRead"
)

// MCPPrefix prefixes the dynamically generated name of every MCP tool,
// which takes the form MCP_<server>_<tool>. The half after the prefix is
// the server's own tool name, which Angela does not choose and cannot
// re-case.
const MCPPrefix = "MCP_"
