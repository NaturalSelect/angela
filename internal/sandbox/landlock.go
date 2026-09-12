package sandbox

import (
	"fmt"

	"github.com/landlock-lsm/go-landlock/landlock"
)

// LandlockSandbox restricts the process using the Linux Landlock LSM.
// On platforms or kernels without Landlock support, EnterSandbox
// degrades to a safe no-op rather than failing.
type LandlockSandbox struct{}

// IsInSandbox reports whether EnterSandbox has already restricted
// this process, on either backend; see sandbox.go's entered.
func (LandlockSandbox) IsInSandbox() bool {
	return entered.Load()
}

// EnterSandbox applies cfg using Landlock's best-effort mode: it
// enforces as much as the running kernel supports and never fails
// just because a stronger ABI version isn't available. Landlock has
// no way to restrict network access for only this process's
// children, so cfg.AllowNetwork never touches Landlock directly; see
// ShouldRestrictChildNetwork for how it's enforced instead. A second
// call, on either backend, is a no-op; see sandbox.go's entered doc.
func (LandlockSandbox) EnterSandbox(cfg Config) error {
	if entered.Load() {
		return nil
	}

	rs := resolve(cfg).existing()
	if !rs.empty() {
		rules := make([]landlock.Rule, 0, 4)
		if len(rs.readDirs) > 0 {
			rules = append(rules, landlock.RODirs(rs.readDirs...).IgnoreIfMissing())
		}
		if len(rs.writeDirs) > 0 {
			rules = append(rules, landlock.RWDirs(rs.writeDirs...).IgnoreIfMissing())
		}
		if len(rs.readFiles) > 0 {
			rules = append(rules, landlock.ROFiles(rs.readFiles...).IgnoreIfMissing())
		}
		if len(rs.writeFiles) > 0 {
			rules = append(rules, landlock.RWFiles(rs.writeFiles...).IgnoreIfMissing())
		}
		if err := landlock.V10.BestEffort().RestrictPaths(rules...); err != nil {
			return fmt.Errorf("enter sandbox: restrict paths: %w", err)
		}
	}

	restrictChildNetwork.Store(rs.restrictChildNetwork)
	entered.Store(true)
	return nil
}
