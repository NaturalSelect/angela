package permission

import (
	"path/filepath"
	"strings"

	"github.com/NaturalSelect/angela/internal/filepathext"
)

// maxResolveDepth caps how far resolvePath will walk up a path looking
// for something that exists. It also bounds the work a hostile path of
// thousands of components can cause.
const maxResolveDepth = 40

// resolvePath makes path absolute and resolves every symlink in it,
// parent directories included.
//
// Scope checks compare paths lexically, and a lexical comparison cannot
// see a link: a workspace holding `link -> /etc` makes `link/passwd`
// look like a file inside the workspace, which would auto-approve
// reading /etc/passwd. Resolving first closes that escape.
//
// A path that does not exist yet resolves as far as its nearest
// existing ancestor, which is what writing a new file needs: the file
// is absent but the directory it lands in is real, and that directory
// is the one that could be a link.
func resolvePath(path, cwd string) string {
	if path == "" {
		return ""
	}
	// SmartIsAbs, not filepath.IsAbs: the tools open their files through
	// filepathext.SmartJoin, so on Windows a leading slash already
	// escapes the workspace. Re-anchoring such a path to cwd here would
	// make an outside file look like it sits inside and auto-approve it.
	if !filepathext.SmartIsAbs(path) && cwd != "" {
		path = filepath.Join(cwd, path)
	}
	path = filepath.Clean(path)

	if resolved, ok := resolveExisting(path, maxResolveDepth); ok {
		return resolved
	}
	return path
}

// resolveExisting resolves the longest existing prefix of path and
// re-attaches the components that do not exist yet.
func resolveExisting(path string, depth int) (string, bool) {
	if depth <= 0 {
		return "", false
	}
	// EvalSymlinks reports ELOOP itself, so a link cycle fails here
	// rather than spinning.
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, true
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", false
	}
	resolvedParent, ok := resolveExisting(parent, depth-1)
	if !ok {
		return "", false
	}
	return filepath.Join(resolvedParent, filepath.Base(path)), true
}

// withinDir reports whether path sits inside dir once both sides have
// their symlinks resolved.
func withinDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	return withinResolvedDir(resolvePath(path, ""), resolvePath(dir, ""))
}

// withinResolvedDir compares two paths that are already resolved.
func withinResolvedDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	// Compare whole components, not the leading two characters: a
	// directory legitimately named "..foo" sits inside dir, and only a
	// rel of ".." or one starting with "../" leaves it.
	return rel == "." ||
		(rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
