package sandbox

import (
	"fmt"
	"sync/atomic"

	"github.com/landlock-lsm/go-landlock/landlock"
)

// entered tracks whether LandlockSandbox.EnterSandbox has already
// restricted this process. Landlock confinement is process-wide and
// irreversible, so this is process-global state rather than
// per-instance state.
var entered atomic.Bool

// LandlockSandbox restricts the process using the Linux Landlock LSM.
// On platforms or kernels without Landlock support, EnterSandbox
// degrades to a safe no-op rather than failing.
type LandlockSandbox struct{}

// IsInSandbox reports whether EnterSandbox has already restricted
// this process.
func (LandlockSandbox) IsInSandbox() bool {
	return entered.Load()
}

// EnterSandbox applies cfg using Landlock's best-effort mode: it
// enforces as much as the running kernel supports and never fails
// just because a stronger ABI version isn't available. Landlock has
// no way to restrict network access for only this process's
// children, so cfg.AllowNetwork never touches Landlock; see
// ShouldRestrictChildNetwork for how it's enforced instead.
func (LandlockSandbox) EnterSandbox(cfg Config) error {
	cf := landlock.V10.BestEffort()

	rules := make([]landlock.Rule, 0, 4)
	if len(cfg.ReadOnly) > 0 {
		rules = append(rules, landlock.RODirs(cfg.ReadOnly...).IgnoreIfMissing())
	}
	if len(cfg.ReadWrite) > 0 {
		rules = append(rules, landlock.RWDirs(cfg.ReadWrite...).IgnoreIfMissing())
	}
	if len(rules) > 0 {
		// /dev/null, /dev/zero, /dev/full, /dev/random, and
		// /dev/urandom are safe regardless of the rest of the
		// sandbox (they don't expose or persist anything) and are
		// routinely needed for I/O redirection and random data
		// generation, e.g. "cmd >/dev/null" or "head -c16
		// /dev/urandom". Grant them explicitly: the workspace
		// profile's read-only "/" would otherwise block writing to
		// /dev/null.
		rules = append(rules,
			landlock.RWFiles("/dev/null").IgnoreIfMissing(),
			landlock.ROFiles("/dev/zero", "/dev/full", "/dev/random", "/dev/urandom").IgnoreIfMissing(),
		)
		if err := cf.RestrictPaths(rules...); err != nil {
			return fmt.Errorf("enter sandbox: restrict paths: %w", err)
		}
	}

	if !cfg.AllowNetwork {
		restrictChildNetwork.Store(true)
	}

	entered.Store(true)
	return nil
}
