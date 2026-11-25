package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"

	llsyscall "github.com/landlock-lsm/go-landlock/landlock/syscall"
)

const (
	BackendLandlock = "landlock"
	BackendSeatbelt = "seatbelt"
	BackendDocker   = "docker"
	BackendNone     = "none"
)

// Status is a read-only snapshot of the sandbox this process runs under.
type Status struct {
	Backend string
	// Active reports whether the process is confined, by Angela itself or
	// by an external container.
	Active bool
	// LandlockABI is the kernel's Landlock ABI version, 0 when the kernel
	// has no Landlock support. Only set for BackendLandlock.
	LandlockABI int
	// FilesystemRestricted reports whether EnterSandbox received any path
	// rule. An empty Config enters the sandbox without restricting files.
	FilesystemRestricted bool
	// ReadDirs and WriteDirs are the directories that still exist and are
	// enforced. ReadDirs includes everything in WriteDirs.
	ReadDirs  []string
	WriteDirs []string
	// ReadFiles and WriteFiles are the single files enforced, without the
	// /dev entries every sandbox grants. ReadFiles includes WriteFiles.
	ReadFiles  []string
	WriteFiles []string
	// UnixSockets are the pathname unix sockets that may be connected to.
	UnixSockets []string
	// BlocksChildNetwork reports whether commands the shell tool spawns
	// lose outbound network access.
	BlocksChildNetwork bool
}

// NOTE: Written by EnterSandbox once enforcement succeeded, so a failed
// attempt leaves no rules behind.
var appliedRules atomic.Pointer[ruleSet]

func recordApplied(rs ruleSet) { appliedRules.Store(&rs) }

// NOTE: False outside a sandbox, inside an external container, and after an empty Config.
func Enforced() bool {
	rs := appliedRules.Load()
	return rs != nil && !rs.empty()
}

// NOTE: Answers from the rules Angela applied, so a kernel without socket rules is still refused.
func AllowsUnixSocket(path string) bool {
	return appliedRules.Load().allowsUnixSocket(path)
}

// NOTE: Reports what EnterSandbox(c) would enforce, so a caller can act before the sandbox closes the door.
func (c Config) Restricts() bool {
	return !resolve(c).existing().empty()
}

// NOTE: Answers as AllowsUnixSocket will once EnterSandbox(c) has run.
func (c Config) AllowsUnixSocket(path string) bool {
	rs := resolve(c).existing()
	return rs.allowsUnixSocket(path)
}

func (rs *ruleSet) allowsUnixSocket(path string) bool {
	if rs == nil || rs.empty() {
		return true
	}
	path = filepath.Clean(path)
	target, err := statSocketTarget(path)
	for _, granted := range rs.unixSockets {
		if granted == path {
			return true
		}
		if err != nil {
			continue
		}
		if fi, serr := statSocketTarget(granted); serr == nil && os.SameFile(target, fi) {
			return true
		}
	}
	return false
}

// NOTE: Resolves symlinks first because Windows os.Stat fails when a symlink points to an AF_UNIX socket.
func statSocketTarget(path string) (os.FileInfo, error) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return os.Stat(path)
}

var landlockABI = sync.OnceValue(func() int {
	v, err := llsyscall.LandlockGetABIVersion()
	if err != nil || v < 1 {
		return 0
	}
	return v
})

// CurrentStatus reports the sandbox New(noDockerSandbox) would return and
// whether it is active.
func CurrentStatus(noDockerSandbox bool) Status {
	sb := New(noDockerSandbox)
	st := Status{Active: sb.IsInSandbox()}

	switch sb.(type) {
	case DockerSandbox:
		st.Backend = BackendDocker
		return st
	case LandlockSandbox:
		st.Backend = BackendLandlock
		st.LandlockABI = landlockABI()
	case SeatbeltSandbox:
		st.Backend = BackendSeatbelt
	default:
		st.Backend = BackendNone
		return st
	}
	if !st.Active {
		return st
	}

	st.BlocksChildNetwork = ShouldRestrictChildNetwork()
	if rs := appliedRules.Load(); rs != nil {
		st.FilesystemRestricted = !rs.empty()
		st.ReadDirs = slices.Clone(rs.readDirs)
		st.WriteDirs = slices.Clone(rs.writeDirs)
		st.ReadFiles = withoutPaths(rs.readFiles, safeDevReadFiles)
		st.WriteFiles = withoutPaths(rs.writeFiles, safeDevWriteFiles)
		st.UnixSockets = slices.Clone(rs.unixSockets)
	}
	return st
}

func withoutPaths(paths, drop []string) []string {
	out := slices.DeleteFunc(slices.Clone(paths), func(p string) bool {
		return slices.Contains(drop, p)
	})
	if len(out) == 0 {
		return nil
	}
	return out
}
