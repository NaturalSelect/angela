package permission

import (
	"path/filepath"
	"strings"

	"github.com/NaturalSelect/angela/internal/filepathext"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

// FilesystemAllowPaths derives extra filesystem access from rules'
// unconditional allow entries, for a caller that also enforces an
// OS-level sandbox (see internal/sandbox) and wants it to match what
// these rules already let through without a prompt. Without this, a
// rule permitting edits outside the sandbox's default set would
// still hit the OS restriction, turning an approved edit into a
// confusing I/O error instead of the prompt the user would otherwise
// see.
//
// A pattern that carries a glob wildcard (see ruleDir) contributes a
// directory, since it can match more than one entry under it. A
// pattern with no wildcard anywhere names one literal file, and
// contributes that exact file (see ruleFile) rather than its parent
// directory: widening the whole directory would grant sibling files
// the rule never approved.
//
// Only rules naming a filesystem category ("read", "list", "edit", or
// its "write" alias) or one of the matching built-in tools contribute
// a path; read/list rules land in readOnly and edit rules land in
// readWrite. A rule contributes nothing when its tool is unrelated to
// the filesystem (execute, network, mcp, ...), or when its pattern
// has no literal directory or file to anchor on: empty, "*", a
// pattern that starts with a wildcard, or one that resolves to a
// filesystem root. The last case matters because granting it would
// defeat the sandbox rather than merely widen it to match the rule.
func FilesystemAllowPaths(rules []Rule, cwd string) (readOnlyDirs, readWriteDirs, readOnlyFiles, readWriteFiles []string) {
	for _, rule := range rules {
		if rule.Action != RuleAllow {
			continue
		}
		write, ok := filesystemRuleTool(rule.Tool)
		if !ok {
			continue
		}
		if dir, ok := ruleDir(rule.Pattern, cwd); ok {
			if write {
				readWriteDirs = append(readWriteDirs, dir)
			} else {
				readOnlyDirs = append(readOnlyDirs, dir)
			}
		} else if file, ok := ruleFile(rule.Pattern, cwd); ok {
			if write {
				readWriteFiles = append(readWriteFiles, file)
			} else {
				readOnlyFiles = append(readOnlyFiles, file)
			}
		}
	}
	return readOnlyDirs, readWriteDirs, readOnlyFiles, readWriteFiles
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
// cwd, for a pattern that carries a glob wildcard somewhere (so it
// can match more than one entry under that directory). ok is false
// when pattern has no wildcard at all (see ruleFile for that case),
// carries no literal directory to anchor on, or resolves to a
// filesystem root.
func ruleDir(pattern, cwd string) (dir string, ok bool) {
	if !hasGlobMeta(pattern) {
		return "", false
	}
	prefix, _ := filepathext.SplitGlobPrefix(pattern)
	if prefix == "" {
		return "", false
	}
	dir = resolveRulePath(prefix, cwd)
	if filepath.Dir(dir) == dir {
		return "", false
	}
	return dir, true
}

// ruleFile resolves a rule pattern that names a single literal file,
// i.e. one with no glob wildcard anywhere in it, against cwd. ok is
// false when pattern carries a wildcard instead (see ruleDir), is
// empty, or resolves to a filesystem root.
func ruleFile(pattern, cwd string) (file string, ok bool) {
	if pattern == "" || hasGlobMeta(pattern) {
		return "", false
	}
	file = resolveRulePath(pattern, cwd)
	if filepath.Dir(file) == file {
		return "", false
	}
	return file, true
}

// resolveRulePath resolves p, a literal (non-wildcard) path segment
// of a rule pattern, against cwd: p is joined onto cwd unless it is
// already absolute, then cleaned.
func resolveRulePath(p, cwd string) string {
	if !filepathext.SmartIsAbs(p) {
		p = filepath.Join(cwd, p)
	}
	return filepath.Clean(p)
}

// hasGlobMeta reports whether pattern contains a glob metacharacter
// anywhere, using the same charset and slash-normalization as
// filepathext.SplitGlobPrefix so the two agree on what counts as a
// literal path.
func hasGlobMeta(pattern string) bool {
	return strings.ContainsAny(filepath.ToSlash(pattern), "*?[{\\")
}
