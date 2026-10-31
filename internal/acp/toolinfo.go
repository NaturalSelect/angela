package acp

import (
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/filepathext"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

type toolPresentation struct {
	kind      protocol.ToolKind
	title     string
	locations []protocol.Location
}

// NOTE: The kind depends on the tool name only. A call is announced while its
// input is still empty, and a later update cannot change the kind.
func presentTool(call message.ToolCall, cwd string) toolPresentation {
	title, locations := describeInput(call, cwd)
	if title == "" {
		title = call.Name
	}
	return toolPresentation{kind: toolKind(call.Name), title: title, locations: locations}
}

func toolKind(name string) protocol.ToolKind {
	switch name {
	case toolnames.Read, toolnames.ReadMCPResource, toolnames.LoadReport,
		toolnames.AngelaLogs, toolnames.AngelaInfo:
		return protocol.ToolKindRead
	case toolnames.Edit, toolnames.MultiEdit, toolnames.Write,
		toolnames.LSPReplaceSymbol, toolnames.LSPRename:
		return protocol.ToolKindEdit
	case toolnames.Grep, toolnames.Glob, toolnames.LS, toolnames.Sourcegraph,
		toolnames.LSPReferences, toolnames.LSPDefinition, toolnames.LSPCallHierarchy,
		toolnames.LSPDiagnostics, toolnames.LSPSymbols, toolnames.ListMCPResources:
		return protocol.ToolKindSearch
	case toolnames.Bash, toolnames.Git, toolnames.JobOutput, toolnames.JobKill,
		toolnames.AngelaReload:
		return protocol.ToolKindExecute
	case toolnames.Fetch, toolnames.WebFetch, toolnames.Download,
		toolnames.WebSearch, toolnames.MultiSearch:
		return protocol.ToolKindFetch
	case toolnames.Todos:
		return protocol.ToolKindThink
	default:
		return protocol.ToolKindOther
	}
}

// describeInput returns an empty title when the input cannot say more than
// the tool name does.
func describeInput(call message.ToolCall, cwd string) (string, []protocol.Location) {
	switch call.Name {
	case toolnames.Read:
		in, _ := decodeJSON[tools.ReadParams](call.Input)
		title, locations := fileTitle("Read", in.FilePath, cwd)
		if in.Offset > 0 && len(locations) > 0 {
			locations[0].Line = new(uint32(in.Offset) + 1)
		}
		return title, locations
	case toolnames.Edit:
		in, _ := decodeJSON[tools.EditParams](call.Input)
		return fileTitle("Edit", in.FilePath, cwd)
	case toolnames.MultiEdit:
		in, _ := decodeJSON[tools.MultiEditParams](call.Input)
		return fileTitle("Edit", in.FilePath, cwd)
	case toolnames.LSPReplaceSymbol:
		in, _ := decodeJSON[tools.ReplaceSymbolParams](call.Input)
		return fileTitle("Edit", in.FilePath, cwd)
	case toolnames.Write:
		in, _ := decodeJSON[tools.WriteParams](call.Input)
		return fileTitle("Write", in.FilePath, cwd)
	case toolnames.LSPRename:
		in, _ := decodeJSON[tools.RenameParams](call.Input)
		title := ""
		if in.Symbol != "" && in.NewName != "" {
			title = "Rename " + in.Symbol + " to " + in.NewName
		}
		return title, locationsFor(cwd, in.Path)
	case toolnames.Grep:
		in, _ := decodeJSON[tools.GrepParams](call.Input)
		title := argTitle("Grep", in.Pattern)
		if title != "" && in.Path != "" {
			title += " in " + displayPath(cwd, in.Path)
		}
		return title, locationsFor(cwd, in.Path)
	case toolnames.Glob:
		in, _ := decodeJSON[tools.GlobParams](call.Input)
		return argTitle("Glob", in.Pattern), locationsFor(cwd, in.Path)
	case toolnames.LS:
		in, ok := decodeJSON[tools.LSParams](call.Input)
		if !ok {
			return "", nil
		}
		dir := "."
		if in.Path != "" {
			dir = displayPath(cwd, in.Path)
		}
		return "LS " + dir, locationsFor(cwd, in.Path)
	case toolnames.Sourcegraph:
		in, _ := decodeJSON[tools.SourcegraphParams](call.Input)
		return argTitle("Sourcegraph", in.Query), nil
	case toolnames.LSPReferences:
		in, _ := decodeJSON[tools.ReferencesParams](call.Input)
		return argTitle(call.Name, in.Symbol), locationsFor(cwd, in.Path)
	case toolnames.LSPDefinition:
		in, _ := decodeJSON[tools.DefinitionParams](call.Input)
		return argTitle(call.Name, in.Symbol), locationsFor(cwd, in.Path)
	case toolnames.LSPCallHierarchy:
		in, _ := decodeJSON[tools.CallHierarchyParams](call.Input)
		return argTitle(call.Name, in.Symbol), locationsFor(cwd, in.Path)
	case toolnames.LSPDiagnostics:
		in, _ := decodeJSON[tools.DiagnosticsParams](call.Input)
		return fileTitle(call.Name, in.FilePath, cwd)
	case toolnames.LSPSymbols:
		in, _ := decodeJSON[tools.SymbolsParams](call.Input)
		return fileTitle(call.Name, in.FilePath, cwd)
	case toolnames.Bash:
		in, _ := decodeJSON[tools.BashParams](call.Input)
		return firstLine(in.Command), nil
	case toolnames.Git:
		in, _ := decodeJSON[tools.GitParams](call.Input)
		if len(in.Args) == 0 {
			return "", nil
		}
		return "git " + strings.Join(in.Args, " "), nil
	case toolnames.JobOutput:
		in, _ := decodeJSON[tools.JobOutputParams](call.Input)
		return argTitle(call.Name, in.ShellID), nil
	case toolnames.JobKill:
		in, _ := decodeJSON[tools.JobKillParams](call.Input)
		return argTitle(call.Name, in.ShellID), nil
	case toolnames.Fetch:
		in, _ := decodeJSON[tools.FetchParams](call.Input)
		return in.URL, nil
	case toolnames.WebFetch:
		in, _ := decodeJSON[tools.WebFetchParams](call.Input)
		return in.URL, nil
	case toolnames.Download:
		in, _ := decodeJSON[tools.DownloadParams](call.Input)
		return in.URL, locationsFor(cwd, in.FilePath)
	case toolnames.WebSearch:
		in, _ := decodeJSON[tools.WebSearchParams](call.Input)
		return in.Query, nil
	case toolnames.MultiSearch:
		in, _ := decodeJSON[tools.MultiSearchParams](call.Input)
		return in.Query, nil
	case toolnames.Todos:
		return "Update todos", nil
	case toolnames.Agent:
		in, _ := decodeJSON[agent.AgentParams](call.Input)
		return in.Description, nil
	default:
		return "", nil
	}
}

// toolResultContent turns a finished tool result into what the editor shows
// for the call. call is meaningful only when found is true.
func toolResultContent(r message.ToolResult, call message.ToolCall, found bool, cwd string) []protocol.ToolCallContent {
	if !r.IsError {
		if diffs := resultDiffs(r, call, found, cwd); len(diffs) > 0 {
			return diffs
		}
		if image, ok := resultImage(r); ok {
			return []protocol.ToolCallContent{protocol.ToolContent{Block: image}}
		}
	}
	if r.Content == "" {
		return nil
	}
	return []protocol.ToolCallContent{protocol.ToolContent{Block: protocol.TextBlock{Text: r.Content}}}
}

// NOTE: Edit, MultiEdit and Write metadata carry no file path, and Write
// metadata carries no new content. Both come from the call input.
func resultDiffs(r message.ToolResult, call message.ToolCall, found bool, cwd string) []protocol.ToolCallContent {
	switch r.Name {
	case toolnames.Edit:
		meta, metaOK := decodeJSON[tools.EditResponseMetadata](r.Metadata)
		in, inOK := decodeJSON[tools.EditParams](call.Input)
		if !found || !metaOK || !inOK {
			return nil
		}
		return fileDiff(cwd, in.FilePath, meta.Created, meta.OldContent, meta.NewContent)
	case toolnames.MultiEdit:
		meta, metaOK := decodeJSON[tools.MultiEditResponseMetadata](r.Metadata)
		in, inOK := decodeJSON[tools.MultiEditParams](call.Input)
		if !found || !metaOK || !inOK {
			return nil
		}
		return fileDiff(cwd, in.FilePath, meta.Created, meta.OldContent, meta.NewContent)
	case toolnames.Write:
		meta, metaOK := decodeJSON[tools.WriteResponseMetadata](r.Metadata)
		in, inOK := decodeJSON[tools.WriteParams](call.Input)
		if !found || !metaOK || !inOK {
			return nil
		}
		return fileDiff(cwd, in.FilePath, meta.Created, meta.OldContent, in.Content)
	case toolnames.LSPReplaceSymbol:
		meta, ok := decodeJSON[tools.ReplaceSymbolResponseMetadata](r.Metadata)
		if !ok {
			return nil
		}
		return fileDiff(cwd, meta.FilePath, false, meta.OldContent, meta.NewContent)
	case toolnames.LSPRename:
		meta, ok := decodeJSON[tools.RenameResponseMetadata](r.Metadata)
		if !ok {
			return nil
		}
		var diffs []protocol.ToolCallContent
		for _, f := range meta.Files {
			diffs = append(diffs, fileDiff(cwd, f.Path, false, f.OldContent, f.NewContent)...)
		}
		return diffs
	default:
		return nil
	}
}

func fileDiff(cwd, path string, created bool, oldContent, newContent string) []protocol.ToolCallContent {
	if path == "" {
		return nil
	}
	diff := protocol.ToolDiff{Path: absPath(cwd, path), NewText: newContent}
	if !created {
		diff.OldText = &oldContent
	}
	return []protocol.ToolCallContent{diff}
}

func resultImage(r message.ToolResult) (protocol.ImageBlock, bool) {
	if r.Data == "" || r.MIMEType == "" {
		return protocol.ImageBlock{}, false
	}
	data, err := base64.StdEncoding.DecodeString(r.Data)
	if err != nil {
		return protocol.ImageBlock{}, false
	}
	return protocol.ImageBlock{MIMEType: r.MIMEType, Data: data}, true
}

// decodeJSON returns the zero value when decoding fails, so callers never see
// a half-filled struct.
func decodeJSON[T any](s string) (T, bool) {
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		var zero T
		return zero, false
	}
	return v, true
}

func absPath(cwd, path string) string {
	return filepathext.SmartJoin(cwd, path)
}

func displayPath(cwd, path string) string {
	abs := absPath(cwd, path)
	rel, err := filepath.Rel(cwd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return abs
	}
	return rel
}

func locationsFor(cwd string, paths ...string) []protocol.Location {
	var locations []protocol.Location
	for _, path := range paths {
		if path != "" {
			locations = append(locations, protocol.Location{Path: absPath(cwd, path)})
		}
	}
	return locations
}

func fileTitle(verb, path, cwd string) (string, []protocol.Location) {
	if path == "" {
		return "", nil
	}
	return verb + " " + displayPath(cwd, path), locationsFor(cwd, path)
}

func argTitle(verb, arg string) string {
	if arg == "" {
		return ""
	}
	return verb + " " + arg
}

func firstLine(s string) string {
	line, _, multiline := strings.Cut(strings.TrimSpace(s), "\n")
	line = strings.TrimSpace(line)
	if multiline && line != "" {
		return line + "…"
	}
	return line
}
