//go:build linux || darwin

package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// enforceHelperEnv names the enforceScenarios entry
// runEnforceHelperIfRequested should run in a subprocess.
// enforceHelperDirEnv and enforceHelperDir2Env carry the temp
// directories that scenario's Config and probes need: a subprocess
// started from os.Args[0] alone can't see the parent test's
// t.TempDir(), so the parent passes them explicitly.
const (
	enforceHelperEnv     = "ANGELA_TEST_SANDBOX_ENFORCE"
	enforceHelperDirEnv  = "ANGELA_TEST_SANDBOX_ENFORCE_DIR"
	enforceHelperDir2Env = "ANGELA_TEST_SANDBOX_ENFORCE_DIR2"
)

// enforceProbe is one filesystem or network check an enforce
// scenario runs after EnterSandbox succeeds. run reports the actual,
// live outcome (true meaning allowed), which
// runEnforceHelperIfRequested always prints as "name=OK" or
// "name=DENIED"; want is the outcome TestEnterSandbox_Conformance
// asserts for it. Keeping the two separate means a regression prints
// what actually happened instead of just "expected true, got false".
type enforceProbe struct {
	name string
	want bool
	run  func(dir, dir2 string) bool
}

// enforceScenario is one row of the cross-platform conformance
// table: the same Config, built from the resolver both backends
// share (see profile.go), must produce the same probe outcomes on
// Linux (LandlockSandbox) and macOS (SeatbeltSandbox).
type enforceScenario struct {
	name string
	// config builds the Config to enter, given this scenario's own
	// directory and, for scenarios that probe a second, ungranted
	// directory, dir2.
	config func(dir, dir2 string) Config
	// setup pre-populates dir/dir2 in the parent test process,
	// before EnterSandbox ever runs in the subprocess, e.g. writing
	// the file a ReadWriteFiles grant names.
	setup func(t *testing.T, dir, dir2 string)
	// probes run, in order, once EnterSandbox succeeds.
	probes []enforceProbe
	// wantEnterErr, when non-empty, means EnterSandbox itself must
	// fail with an error containing this substring; probes never run
	// in that case. Only the no_network scenario on macOS sets this.
	wantEnterErr string
}

// enforceScenarios is the table TestEnterSandbox_Conformance and
// runEnforceHelperIfRequested both drive. no_network is the sole
// entry whose expectation depends on runtime.GOOS: Seatbelt cannot
// apply a second, tighter profile to just this process's children
// while the process itself is already sandboxed (see
// SeatbeltSandbox.EnterSandbox), so the same AllowNetwork:false
// Config that narrows Landlock's children on Linux must instead fail
// closed on macOS rather than silently leaving them unrestricted.
func enforceScenarios() []enforceScenario {
	noNetwork := enforceScenario{
		name: "no_network",
		config: func(dir, _ string) Config {
			return Config{ReadOnly: []string{"/"}, ReadWrite: []string{dir}, AllowNetwork: false}
		},
	}
	if runtime.GOOS == "darwin" {
		noNetwork.wantEnterErr = "not supported on macOS"
	} else {
		noNetwork.probes = []enforceProbe{
			{name: "restrict_children", want: true, run: func(_, _ string) bool {
				return ShouldRestrictChildNetwork()
			}},
		}
	}

	return []enforceScenario{
		{
			name: "workspace",
			config: func(dir, _ string) Config {
				return Config{ReadOnly: []string{"/"}, ReadWrite: []string{dir}, AllowNetwork: true}
			},
			setup: func(t *testing.T, _, dir2 string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir2, "probe.txt"), []byte("x"), 0o644))
			},
			probes: []enforceProbe{
				{name: "write_dir", want: true, run: func(dir, _ string) bool {
					return probeWrite(filepath.Join(dir, "probe.txt"))
				}},
				{name: "read_dir2", want: true, run: func(_, dir2 string) bool {
					return probeRead(filepath.Join(dir2, "probe.txt"))
				}},
				{name: "write_dir2", want: false, run: func(_, dir2 string) bool {
					return probeWrite(filepath.Join(dir2, "probe2.txt"))
				}},
				{name: "write_devnull", want: true, run: func(_, _ string) bool {
					return probeWrite("/dev/null")
				}},
				{name: "read_urandom", want: true, run: func(_, _ string) bool {
					return probeRead("/dev/urandom")
				}},
			},
		},
		{
			name: "file_grant_rw",
			config: func(dir, _ string) Config {
				return Config{ReadWriteFiles: []string{filepath.Join(dir, "key.txt")}, AllowNetwork: true}
			},
			setup: func(t *testing.T, dir, _ string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "key.txt"), []byte("secret"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "other.txt"), []byte("sibling"), 0o644))
			},
			probes: []enforceProbe{
				{name: "write_key", want: true, run: func(dir, _ string) bool {
					return probeWrite(filepath.Join(dir, "key.txt"))
				}},
				{name: "read_key", want: true, run: func(dir, _ string) bool {
					return probeRead(filepath.Join(dir, "key.txt"))
				}},
				{name: "write_other", want: false, run: func(dir, _ string) bool {
					return probeWrite(filepath.Join(dir, "other.txt"))
				}},
				{name: "read_other", want: false, run: func(dir, _ string) bool {
					return probeRead(filepath.Join(dir, "other.txt"))
				}},
				{name: "create_new", want: false, run: func(dir, _ string) bool {
					return probeCreate(filepath.Join(dir, "new.txt"))
				}},
			},
		},
		{
			name: "file_grant_ro",
			config: func(dir, _ string) Config {
				return Config{ReadOnlyFiles: []string{filepath.Join(dir, "key.txt")}, AllowNetwork: true}
			},
			setup: func(t *testing.T, dir, _ string) {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "key.txt"), []byte("secret"), 0o644))
			},
			probes: []enforceProbe{
				{name: "read_key", want: true, run: func(dir, _ string) bool {
					return probeRead(filepath.Join(dir, "key.txt"))
				}},
				{name: "write_key", want: false, run: func(dir, _ string) bool {
					return probeWrite(filepath.Join(dir, "key.txt"))
				}},
			},
		},
		{
			name: "dev_files",
			config: func(_, _ string) Config {
				return Config{ReadOnly: []string{"/"}, AllowNetwork: true}
			},
			probes: []enforceProbe{
				{name: "write_devnull", want: true, run: func(_, _ string) bool {
					return probeWrite("/dev/null")
				}},
				{name: "read_zero", want: true, run: func(_, _ string) bool {
					return probeRead("/dev/zero")
				}},
				{name: "read_full", want: true, run: func(_, _ string) bool {
					return probeRead("/dev/full")
				}},
				{name: "read_random", want: true, run: func(_, _ string) bool {
					return probeRead("/dev/random")
				}},
				{name: "read_urandom", want: true, run: func(_, _ string) bool {
					return probeRead("/dev/urandom")
				}},
				{name: "write_dir", want: false, run: func(dir, _ string) bool {
					return probeWrite(filepath.Join(dir, "probe.txt"))
				}},
			},
		},
		{
			name: "missing_file_grant",
			config: func(dir, _ string) Config {
				return Config{ReadOnly: []string{"/"}, ReadWriteFiles: []string{filepath.Join(dir, "does-not-exist.txt")}, AllowNetwork: true}
			},
			probes: []enforceProbe{
				{name: "create_missing", want: false, run: func(dir, _ string) bool {
					return probeCreate(filepath.Join(dir, "does-not-exist.txt"))
				}},
			},
		},
		noNetwork,
	}
}

// probeWrite reports whether path can be opened for writing (created
// if it doesn't exist yet) and written to.
func probeWrite(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return false
	}
	defer f.Close()
	_, err = f.Write([]byte("x"))
	return err == nil
}

// probeRead reports whether path can be opened for reading and read
// from.
func probeRead(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 4)
	_, err = f.Read(buf)
	return err == nil
}

// probeCreate reports whether a brand-new file at path (which must
// not already exist) can be created, and removes it immediately if
// so.
func probeCreate(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return false
	}
	f.Close()
	os.Remove(path)
	return true
}

// runEnforceHelperIfRequested reports whether this process's
// environment names one of enforceScenarios' entries, and if so
// enters that scenario's sandbox for real (via New, exactly as
// production code would) and runs its probes, printing one
// "name=OK"/"name=DENIED" line per probe, or "ENTER_FAILED: <err>" if
// EnterSandbox itself fails, before reporting its exit code. See
// TestMain in main_test.go for why this runs in a disposable
// subprocess: entering one of these sandboxes for real is
// irreversible and would break every later test in this binary that
// needs the restricted access. noDockerSandbox is true so a
// containerized CI runner still exercises real Landlock enforcement
// instead of trusting the container's own isolation.
func runEnforceHelperIfRequested() (int, bool) {
	name := os.Getenv(enforceHelperEnv)
	if name == "" {
		return 0, false
	}

	var scenario enforceScenario
	found := false
	for _, s := range enforceScenarios() {
		if s.name == name {
			scenario, found = s, true
			break
		}
	}
	if !found {
		fmt.Println("UNKNOWN_SCENARIO:", name)
		return 10, true
	}

	dir, dir2 := os.Getenv(enforceHelperDirEnv), os.Getenv(enforceHelperDir2Env)
	err := New(true).EnterSandbox(scenario.config(dir, dir2))

	if scenario.wantEnterErr != "" {
		if err == nil {
			fmt.Println("ENTER_UNEXPECTEDLY_SUCCEEDED")
			return 10, true
		}
		fmt.Println("ENTER_FAILED:", err)
		return 0, true
	}
	if err != nil {
		fmt.Println("ENTER_FAILED:", err)
		return 10, true
	}

	for _, p := range scenario.probes {
		if p.run(dir, dir2) {
			fmt.Println(p.name + "=OK")
		} else {
			fmt.Println(p.name + "=DENIED")
		}
	}
	return 0, true
}

// TestEnterSandbox_Conformance is this package's proof of its own
// unification goal: for every scenario in enforceScenarios, the same
// Config produces the same effective permissions through whichever
// real backend New returns for the running platform (LandlockSandbox
// on Linux, SeatbeltSandbox on macOS), with no_network's documented,
// GOOS-keyed exception being the only place they're allowed to
// differ. Each scenario runs in its own disposable subprocess (see
// runEnforceHelperIfRequested) because entering a real sandbox is
// irreversible.
func TestEnterSandbox_Conformance(t *testing.T) {
	t.Parallel()

	for _, scenario := range enforceScenarios() {
		t.Run(scenario.name, func(t *testing.T) {
			t.Parallel()

			dir, dir2 := t.TempDir(), t.TempDir()
			if scenario.setup != nil {
				scenario.setup(t, dir, dir2)
			}

			cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^$")
			cmd.Env = append(os.Environ(),
				enforceHelperEnv+"="+scenario.name,
				enforceHelperDirEnv+"="+dir,
				enforceHelperDir2Env+"="+dir2,
			)
			output, err := cmd.CombinedOutput()
			out := string(output)

			if scenario.wantEnterErr != "" {
				require.NoError(t, err, "helper subprocess output: %s", out)
				require.Contains(t, out, "ENTER_FAILED:")
				require.Contains(t, out, scenario.wantEnterErr)
				return
			}

			require.NoError(t, err, "helper subprocess output: %s", out)
			for _, p := range scenario.probes {
				result := "DENIED"
				if p.want {
					result = "OK"
				}
				require.Contains(t, out, p.name+"="+result)
			}
		})
	}
}
