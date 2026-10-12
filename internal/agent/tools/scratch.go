package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/NaturalSelect/angela/internal/session"
)

// plainScratchName is what a session ID may look like to name its scratch
// directory as is. Anything else could change meaning once a path built
// from it is pasted into a shell command, as "$$" does.
var plainScratchName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// SessionScratchDir returns the scratch subdirectory for one session's
// saved files under root, or an error if sessionID is not safe to use as
// a single path component.
//
// For delegated agents, sessionID is built from a provider-supplied
// tool-call ID (coordinator.CreateAgentToolSessionID), which this
// package cannot trust: filepath.Join normalizes rather than sandboxes,
// so a value containing a path separator or a "." / ".." segment would
// otherwise let a malicious provider response point the join outside
// root. Every writer that creates such a directory and the coordinator
// that later removes it call this, so none of them can disagree about
// what counts as safe.
//
// The model is handed paths in this directory and may pass them to Bash.
// An ID with shell syntax in it, such as the "$$" of a delegated session,
// would then expand to something else, so only plain IDs name the
// directory themselves and every other ID is replaced by its hash.
func SessionScratchDir(root, sessionID string) (string, error) {
	if sessionID == "" || sessionID == "." || sessionID == ".." || strings.ContainsAny(sessionID, `/\`) {
		return "", fmt.Errorf("unsafe session id %q for scratch directory", sessionID)
	}
	name := sessionID
	if !plainScratchName.MatchString(sessionID) {
		name = session.HashID(sessionID)
	}
	return filepath.Join(root, name), nil
}

// NOTE: The path is handed to the model, so saved files must outlive the
// turn that wrote them.
const SessionScratchRetention = 24 * time.Hour

// PruneSessionScratch removes session directories under root whose last
// file was written more than maxAge ago. A missing root is not an error.
func PruneSessionScratch(root string, maxAge time.Duration) error {
	entries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("listing scratch root: %w", err)
	}

	cutoff := time.Now().Add(-maxAge)
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
