//go:build darwin

package sandbox

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

// sandboxInit and sandboxFreeError mirror libSystem's sandbox_init(3)
// and sandbox_free_error(3):
//
//	int sandbox_init(const char *profile, uint64_t flags, char **errorbuf);
//	void sandbox_free_error(char *errorbuf);
//
// sandbox_init is the same primitive sandbox-exec(1) itself calls
// after parsing its own command line; calling it directly on the
// running process, instead of relaunching under sandbox-exec, means
// the profile applies in place, the same way LandlockSandbox applies
// Landlock rules in place, with no relaunch and no window where
// entering the sandbox stops being safe.
var (
	sandboxInit      func(profile string, flags uint64, errorbuf **byte) int32
	sandboxFreeError func(errorbuf *byte)
)

// loadSandboxFuncs resolves sandbox_init and sandbox_free_error from
// libSystem, which every darwin process already has loaded (Go's own
// darwin runtime links against it), so RTLD_DEFAULT finds them
// without needing an explicit Dlopen call.
var loadSandboxFuncs = sync.OnceValue(func() error {
	initSym, err := purego.Dlsym(purego.RTLD_DEFAULT, "sandbox_init")
	if err != nil {
		return fmt.Errorf("resolve sandbox_init: %w", err)
	}
	freeSym, err := purego.Dlsym(purego.RTLD_DEFAULT, "sandbox_free_error")
	if err != nil {
		return fmt.Errorf("resolve sandbox_free_error: %w", err)
	}
	purego.RegisterFunc(&sandboxInit, initSym)
	purego.RegisterFunc(&sandboxFreeError, freeSym)
	return nil
})

// applySeatbeltProfile installs profile on the current process via
// sandbox_init(3), the deprecated-but-still-present API
// sandbox-exec(1) itself is built on. The restriction is irreversible
// and process-wide for the rest of the process's life, and inherited
// by every child, the same as Landlock's RestrictPaths.
func applySeatbeltProfile(profile string) error {
	if err := loadSandboxFuncs(); err != nil {
		return err
	}

	var errorbuf *byte
	if ret := sandboxInit(profile, 0, &errorbuf); ret != 0 {
		if errorbuf == nil {
			return fmt.Errorf("sandbox_init failed with no error message (return %d)", ret)
		}
		msg := unix.BytePtrToString(errorbuf)
		sandboxFreeError(errorbuf)
		return fmt.Errorf("sandbox_init: %s", msg)
	}
	return nil
}
