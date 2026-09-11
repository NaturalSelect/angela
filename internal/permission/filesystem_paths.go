package permission

import (
	"path/filepath"

	"github.com/NaturalSelect/angela/internal/filepathext"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

// FilesystemAllowPaths derives extra filesystem directories from
// rules' unconditional allow entries, for a caller that also enforces
// an OS-level sandbox (see internal/sandbox) and wants it to match
// what these rules already let through without a prompt. Without
// this, a rule permitting edits outside the sandbox's default set
// would still hit the OS restriction, turning an approved edit into a
// confusing I/O error instead of the prompt the user would otherwise
// see.
//
// Only rules naming a filesystem category ("read", "list", "edit", or
// its "write" alias) or one of the matching built-in tools contribute
// a path; read/list rules land in readOnly and edit rules land in
// readWrite. A rule contributes nothing when its tool is unrelated to
// the filesystem (execute, network, mcp, ...), or when its pattern
// has no literal directory to anchor on: empty, "*", a pattern that
// starts with a wildcard, or one that resolves to a filesystem root.
// The last case matters because granting it would defeat the sandbox
// rather than merely widen it to match the rule.
func FilesystemAllowPaths(rules []Rule, cwd string) (readOnly, readWrite []string) {
	for _, rule := range rules {
		if rule.Action != RuleAllow {
			continue
		}
		write, ok := filesystemRuleTool(rule.Tool)
		if !ok {
			continue
		}
		dir, ok := ruleDir(rule.Pattern, cwd)
		if !ok {
			continue
		}
		if write {
			readWrite = append(readWrite, dir)
		} else {
			readOnly = append(readOnly, dir)
		}
	}
	return readOnly, readWrite
}

// filesystemRuleTool reports whether tool names a filesystem-facing
// access category or built-in tool, and if so whether it can write as
// well as read.
func filesystemRuleTool(tool string) (write, ok bool) {
	switch tool {
	case toolnames.Write, toolnames.Edit, toolnames.MultiEdit:
		return true, true
	case toolnames.View, toolnames.Glob, toolnames.Grep, toolnames.LS:
		return false, true
	}
	action, matched := ParseAction(tool)
	if !matched {
		return false, false
	}
	switch action {
	case ActionEdit:
		return true, true
	case ActionRead, ActionList:
		return false, true
	default:
		return false, false
	}
}

// ruleDir resolves a rule pattern's literal leading directory against
// cwd. ok is false when the pattern carries no such directory, or
// when it resolves to a filesystem root.
func ruleDir(pattern, cwd string) (dir string, ok bool) {
	prefix, _ := filepathext.SplitGlobPrefix(pattern)
	if prefix == "" {
		return "", false
	}
	dir = prefix
	if !filepathext.SmartIsAbs(dir) {
		dir = filepath.Join(cwd, dir)
	}
	dir = filepath.Clean(dir)
	if filepath.Dir(dir) == dir {
		return "", false
	}
	return dir, true
}
