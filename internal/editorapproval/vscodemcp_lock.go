package editorapproval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// lockFile mirrors the JSON that VS Code's Copilot Chat extension writes
// to advertise its local MCP diff-review endpoint for a running window
// (extensions/copilot/.../vscode-node/lockFile.ts upstream). Only the
// fields Angela needs are decoded; unknown fields are ignored.
type lockFile struct {
	SocketPath       string            `json:"socketPath"`
	Scheme           string            `json:"scheme"`
	Headers          map[string]string `json:"headers"`
	PID              int               `json:"pid"`
	IDEName          string            `json:"ideName"`
	Timestamp        int64             `json:"timestamp"`
	WorkspaceFolders []string          `json:"workspaceFolders"`
	IsTrusted        bool              `json:"isTrusted"`
}

// lockDir returns the directory VS Code's Copilot Chat extension writes
// its lock files to, mirroring upstream's node/cliHelpers.ts
// getCopilotCliStateDir: $COPILOT_HOME/ide, defaulting COPILOT_HOME to
// ~/.copilot.
func lockDir(getenv func(string) string) string {
	home := getenv("COPILOT_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(userHome, ".copilot")
	}
	return filepath.Join(home, "ide")
}

// findLock picks the best lock file in dir for workingDir: its process
// must still be alive, its transport must be one Angela can dial, and
// workingDir must be inside one of its advertised workspace folders.
// When several match, the most recently written one wins. Unreadable or
// malformed lock files are skipped rather than treated as fatal, since
// they can be left behind by a VS Code window that crashed or is
// mid-shutdown.
func findLock(dir, workingDir string) (lockFile, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return lockFile{}, false
	}

	best := lockFile{}
	found := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".lock") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var lock lockFile
		if err := json.Unmarshal(data, &lock); err != nil {
			continue
		}

		if lock.SocketPath == "" || (lock.Scheme != "unix" && lock.Scheme != "pipe") {
			continue
		}
		if !processAlive(lock.PID) {
			continue
		}
		if !containsWorkspace(lock.WorkspaceFolders, workingDir) {
			continue
		}

		if !found || lock.Timestamp > best.Timestamp {
			best = lock
			found = true
		}
	}
	return best, found
}

// containsWorkspace reports whether workingDir is inside any of folders.
func containsWorkspace(folders []string, workingDir string) bool {
	for _, f := range folders {
		if isWithin(f, workingDir) {
			return true
		}
	}
	return false
}

// resolvePath cleans p and resolves symlinks, so path comparisons aren't
// fooled by e.g. /tmp vs /private/tmp on macOS. It falls back to the
// cleaned path when symlinks can't be resolved.
func resolvePath(p string) string {
	cleaned := filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return resolved
	}
	return cleaned
}

// isWithin reports whether workingDir equals folder or is nested inside
// it. It tries resolvePath's output first, then falls back to comparing
// the plain Clean-ed paths. The fallback matters on Windows: resolving
// a path there normalizes each component through a separate FindFirstFile
// call (to expand 8.3 short names like RUNNER~1 and fix case), and on CI
// runners that lookup has been observed to succeed for one side of a
// comparison and not the other, which would otherwise turn two paths
// that are lexically identical (or nested) into a false negative.
func isWithin(folder, workingDir string) bool {
	if pathContains(resolvePath(folder), resolvePath(workingDir)) {
		return true
	}
	return pathContains(filepath.Clean(folder), filepath.Clean(workingDir))
}

// pathContains reports whether workingDir equals folder or is nested
// inside it. Both arguments must already be cleaned.
func pathContains(folder, workingDir string) bool {
	if folder == workingDir {
		return true
	}
	rel, err := filepath.Rel(folder, workingDir)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
