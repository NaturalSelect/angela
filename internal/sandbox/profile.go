package sandbox

import "os"

// safeDevWriteFiles and safeDevReadFiles list device files that stay
// accessible regardless of the rest of a ruleSet: they don't expose or
// persist anything, and are routinely needed for I/O redirection and
// random data generation, e.g. "cmd >/dev/null" or "head -c16
// /dev/urandom". Without them, a read-only "/" grant would otherwise
// block writing to /dev/null. Only granted once at least one user path
// is configured; see resolve.
var (
	safeDevWriteFiles = []string{"/dev/null"}
	safeDevReadFiles  = []string{"/dev/zero", "/dev/full", "/dev/random", "/dev/urandom"}
)

// ruleSet is the platform-neutral, fully resolved form of a Config:
// exactly what both LandlockSandbox and SeatbeltSandbox enforce.
// Translating a Config into a ruleSet in this one shared place is what
// keeps "same Config" meaning "same effective permissions" on both
// platforms, instead of leaving each backend to interpret the four
// path fields on its own.
type ruleSet struct {
	// readDirs is cfg.ReadOnly union cfg.ReadWrite: a directory
	// granted read-write is inherently also readable.
	readDirs []string
	// writeDirs is cfg.ReadWrite.
	writeDirs []string
	// readFiles is cfg.ReadOnlyFiles union cfg.ReadWriteFiles, plus
	// safeDevReadFiles once rs is not empty.
	readFiles []string
	// writeFiles is cfg.ReadWriteFiles, plus safeDevWriteFiles once
	// rs is not empty.
	writeFiles []string
	// restrictChildNetwork mirrors !cfg.AllowNetwork.
	restrictChildNetwork bool
}

// resolve computes the ruleSet cfg implies. It never touches the
// filesystem; call existing() on the result immediately before
// enforcing it to drop paths that don't exist. A Config with none of
// its four path fields set resolves to a ruleSet with empty() true
// and no /dev grants either, so entering a sandbox with a bare
// Config{} still restricts nothing on either platform, matching both
// backends' long-standing behavior.
func resolve(cfg Config) ruleSet {
	rs := ruleSet{
		readDirs:             DedupePaths(append(append([]string{}, cfg.ReadOnly...), cfg.ReadWrite...)),
		writeDirs:            DedupePaths(append([]string{}, cfg.ReadWrite...)),
		readFiles:            DedupePaths(append(append([]string{}, cfg.ReadOnlyFiles...), cfg.ReadWriteFiles...)),
		writeFiles:           DedupePaths(append([]string{}, cfg.ReadWriteFiles...)),
		restrictChildNetwork: !cfg.AllowNetwork,
	}
	if rs.empty() {
		return rs
	}
	rs.readFiles = DedupePaths(append(append([]string{}, rs.readFiles...), safeDevReadFiles...))
	rs.writeFiles = DedupePaths(append(append([]string{}, rs.writeFiles...), safeDevWriteFiles...))
	return rs
}

// empty reports whether rs carries no path rules at all.
func (rs ruleSet) empty() bool {
	return len(rs.readDirs) == 0 && len(rs.writeDirs) == 0 && len(rs.readFiles) == 0 && len(rs.writeFiles) == 0
}

// existing returns a copy of rs with every path that fails os.Stat
// removed, so a Config naming a not-yet-created path (see
// warnMissingSandboxPaths in internal/cmd) drops that rule on both
// platforms instead of just the one backend whose enforcement
// mechanism happens to tolerate it natively.
func (rs ruleSet) existing() ruleSet {
	return ruleSet{
		readDirs:             filterExisting(rs.readDirs),
		writeDirs:            filterExisting(rs.writeDirs),
		readFiles:            filterExisting(rs.readFiles),
		writeFiles:           filterExisting(rs.writeFiles),
		restrictChildNetwork: rs.restrictChildNetwork,
	}
}

func filterExisting(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			out = append(out, p)
		}
	}
	return out
}
