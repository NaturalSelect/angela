package sandbox

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// seatbeltMarkerEnv, when set to "1" in this process's environment,
// means a parent already relaunched it under sandbox-exec (see
// SeatbeltSandbox.EnterSandbox): the Seatbelt restriction persists
// across exec and is inherited by every child, so its presence is
// both how this process recognizes it's already confined and how a
// command the shell tool later spawns would correctly see itself as
// confined too. Unlike child_net.go's childExecMarker, this lives in
// the environment rather than argv[1]: relaunching here replaces the
// entire process, including Cobra's flag parsing, so argv must stay
// exactly what the user typed for that parsing to succeed the second
// time around.
const seatbeltMarkerEnv = "__ANGELA_SANDBOX_SEATBELT__"

// sandboxExecPath is the system sandbox-exec binary EnterSandbox
// relaunches through. Apple's man page marks it deprecated, but as of
// the current release it's still installed by default, and it's the
// only way to apply a Seatbelt profile without cgo, which this
// project builds without.
const sandboxExecPath = "/usr/bin/sandbox-exec"

// privatePrefixes lists macOS top-level directories that are actually
// symlinks into /private. The kernel resolves a path before matching
// it against an SBPL rule, so a rule written against one spelling
// (e.g. "/tmp/x") never matches the path the kernel actually checks
// once resolved ("/private/tmp/x"), and a caller-supplied path might
// arrive in either form. seatbeltPathForms emits both.
var privatePrefixes = []string{"/tmp", "/var", "/etc"}

// SeatbeltSandbox restricts the process using macOS's Seatbelt
// (sandbox-exec) mechanism. Unlike LandlockSandbox, which restricts
// the running process in place, Seatbelt can only be applied by
// relaunching the process under sandbox-exec: EnterSandbox never
// returns on success, replacing this process with a sandboxed re-exec
// of the same binary and arguments. That relaunch discards any
// in-memory state, so it only ever attempts one before
// MarkStartupComplete is called; see internal/cmd/root.go for the one
// caller early enough for that to be safe. Later callers, notably the
// /sandbox TUI command, get ErrNotSupported instead.
type SeatbeltSandbox struct{}

// inSeatbeltSandbox reports whether this process, or the parent that
// spawned it, already entered a Seatbelt sandbox.
func inSeatbeltSandbox() bool {
	return os.Getenv(seatbeltMarkerEnv) == "1"
}

// IsInSandbox reports whether this process already entered a Seatbelt
// sandbox, by checking for the marker EnterSandbox's relaunch leaves
// in the environment.
func (SeatbeltSandbox) IsInSandbox() bool {
	return inSeatbeltSandbox()
}

// EnterSandbox applies cfg by relaunching the process under
// sandbox-exec with an SBPL profile built from cfg; see the
// SeatbeltSandbox doc for why that only ever happens once, at
// startup.
//
// Seatbelt has no way to restrict network access for only this
// process's children the way ShouldRestrictChildNetwork's callers
// expect: doing so would also have to restrict this process's own
// network, which Angela needs for its own provider calls. So unlike
// LandlockSandbox, cfg.AllowNetwork false here only logs a warning;
// it never sets restrictChildNetwork, and commands the shell tool
// spawns keep outbound network access.
func (SeatbeltSandbox) EnterSandbox(cfg Config) error {
	if inSeatbeltSandbox() {
		return nil
	}
	if startupComplete.Load() {
		return fmt.Errorf("enter sandbox: macOS sandbox can only be entered at startup, via --sandbox: %w", ErrNotSupported)
	}

	if !cfg.AllowNetwork {
		slog.Warn("Sandbox network restriction is not supported on macOS; commands run under --sandbox keep outbound network access")
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("enter sandbox: resolve angela executable: %w", err)
	}
	profile, err := seatbeltProfile(cfg, exe)
	if err != nil {
		return fmt.Errorf("enter sandbox: build profile: %w", err)
	}
	if err := relaunchUnderSeatbelt(profile, exe); err != nil {
		return fmt.Errorf("enter sandbox: relaunch under sandbox-exec: %w", err)
	}
	return nil
}

// seatbeltProfile renders cfg as an SBPL (Sandbox Profile Language)
// document for sandbox-exec. exe is the Angela binary being
// relaunched: the profile must explicitly allow reading it, and the
// dynamic linker and system libraries any command the shell tool
// later spawns needs, or the relaunch below replaces this process
// with one unable to read its own executable, and Angela never
// starts.
//
// Mirrors LandlockSandbox.EnterSandbox: with no rules to add, it
// returns the fully permissive "(allow default)" profile instead of
// a deny-everything one, so relaunching under an empty sandbox still
// marks IsInSandbox true without restricting anything, matching
// Landlock's own no-op-when-empty behavior.
// seatbeltProfile renders cfg as an SBPL (Sandbox Profile Language)
// document for sandbox-exec. exe is the Angela binary being
// relaunched: the profile must explicitly allow reading it, and the
// dynamic linker and system libraries any command the shell tool
// later spawns needs, or the relaunch below replaces this process
// with one unable to read its own executable, and Angela never
// starts.
//
// Mirrors LandlockSandbox.EnterSandbox: with no rules to add, it
// returns the fully permissive "(allow default)" profile instead of
// a deny-everything one, so relaunching under an empty sandbox still
// marks IsInSandbox true without restricting anything, matching
// Landlock's own no-op-when-empty behavior. ReadOnlyFiles and
// ReadWriteFiles get "literal" rules rather than the "subpath" rules
// ReadOnly and ReadWrite use, so a single-file grant can't be
// tricked into covering every other file in its parent directory.
func seatbeltProfile(cfg Config, exe string) (string, error) {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")

	if len(cfg.ReadOnly) == 0 && len(cfg.ReadWrite) == 0 && len(cfg.ReadOnlyFiles) == 0 && len(cfg.ReadWriteFiles) == 0 {
		return b.String(), nil
	}

	b.WriteString("(deny file-read* file-write*)\n")
	b.WriteString("(allow file-read-metadata)\n")

	readableDirs := DedupePaths(append(append([]string{}, cfg.ReadOnly...), cfg.ReadWrite...))
	if err := writePathRule(&b, "file-read*", "subpath", readableDirs); err != nil {
		return "", err
	}
	if err := writePathRule(&b, "file-write*", "subpath", cfg.ReadWrite); err != nil {
		return "", err
	}
	readableFiles := DedupePaths(append(append([]string{}, cfg.ReadOnlyFiles...), cfg.ReadWriteFiles...))
	if err := writePathRule(&b, "file-read*", "literal", readableFiles); err != nil {
		return "", err
	}
	if err := writePathRule(&b, "file-write*", "literal", cfg.ReadWriteFiles); err != nil {
		return "", err
	}
	if err := writePathRule(&b, "file-read*", "literal", []string{exe}); err != nil {
		return "", err
	}

	// Angela itself is a static Go binary and needs none of this, but
	// a dynamically linked command the shell tool spawns (git, sh,
	// ...) does, for the dynamic linker, its shared caches, and the
	// system libraries it links against. macOS has no truly static
	// binaries either: even a CGO_ENABLED=0 Go binary dynamically
	// links libSystem, so dyld re-bootstraps this very process on
	// every relaunch under sandbox-exec. A profile that only opened
	// narrow subpaths here (e.g. just /Library/Apple/usr/lib and a
	// couple of /private/var/db entries) let dyld abort with SIGABRT
	// before Go code ever ran again, because its shared cache and
	// code-signature checks reach more broadly into /Library and
	// /private than any fixed list of subpaths anticipates. Granting
	// both trees in full keeps that from being a moving target.
	b.WriteString(`(allow file-read* (subpath "/System") (subpath "/usr/lib") (subpath "/usr/share") (subpath "/Library") (subpath "/private"))` + "\n")

	// /dev/null, /dev/zero, /dev/full, /dev/random, and /dev/urandom
	// are safe regardless of the rest of the sandbox (they don't
	// expose or persist anything) and are routinely needed for I/O
	// redirection and random data generation, e.g. "cmd >/dev/null"
	// or "head -c16 /dev/urandom". Grant them explicitly: a read-only
	// "/" would otherwise block writing to /dev/null. Mirrors
	// LandlockSandbox.EnterSandbox's identical exception.
	b.WriteString(`(allow file-write* (literal "/dev/null"))` + "\n")
	b.WriteString(`(allow file-read* (literal "/dev/zero") (literal "/dev/full") (literal "/dev/random") (literal "/dev/urandom"))` + "\n")

	return b.String(), nil
}

// writePathRule appends "(allow op (kind "f1") (kind "f2") ...)" to b,
// one kind clause per alias form (see seatbeltPathForms) of every path
// in paths, or does nothing if paths is empty.
func writePathRule(b *strings.Builder, op, kind string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	fmt.Fprintf(b, "(allow %s", op)
	for _, p := range paths {
		for _, form := range seatbeltPathForms(p) {
			q, err := seatbeltQuote(form)
			if err != nil {
				return fmt.Errorf("sandbox path %q: %w", p, err)
			}
			fmt.Fprintf(b, " (%s %s)", kind, q)
		}
	}
	b.WriteString(")\n")
	return nil
}

// seatbeltPathForms returns p, cleaned, plus every alias the kernel
// might resolve it to or from: the /private/... form of a path under
// one of privatePrefixes (or the reverse), and the result of
// resolving any symlinks in p, when that differs from p itself (e.g.
// os.TempDir() on macOS is under a per-user symlinked path).
// seatbeltPathForms returns p, cleaned, plus every alias the kernel
// might resolve it to or from: the /private/... form of a path under
// one of privatePrefixes (or the reverse), and the result of
// resolving any symlinks in p, when that differs from p itself (e.g.
// os.TempDir() on macOS is under a per-user symlinked path). Every
// returned form is forward-slash: SBPL is macOS-only and always uses
// "/", regardless of the separator convention of whatever OS happens
// to be building and running this package's tests.
func seatbeltPathForms(p string) []string {
	nativeClean := filepath.Clean(p)
	clean := filepath.ToSlash(nativeClean)
	forms := []string{clean}

	for _, prefix := range privatePrefixes {
		if rest, ok := strings.CutPrefix(clean, prefix); ok && (rest == "" || rest[0] == '/') {
			forms = append(forms, "/private"+clean)
			break
		}
		if rest, ok := strings.CutPrefix(clean, "/private"+prefix); ok && (rest == "" || rest[0] == '/') {
			forms = append(forms, strings.TrimPrefix(clean, "/private"))
			break
		}
	}

	if resolved, err := filepath.EvalSymlinks(nativeClean); err == nil {
		if resolved = filepath.ToSlash(resolved); resolved != clean {
			forms = append(forms, resolved)
		}
	}

	return DedupePaths(forms)
}

// seatbeltQuote renders s as an SBPL string literal: backslashes and
// double quotes are escaped, and control characters, which SBPL has
// no escape for, are rejected outright rather than silently admitted
// into a profile they could otherwise break out of.
func seatbeltQuote(s string) (string, error) {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("path contains control character %q", r)
		}
	}
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(s) + `"`, nil
}

// seatbeltRelaunchArgv returns the argv sandbox-exec should be run
// with to apply profile and then exec into exe with args (typically
// Angela's own os.Args[1:]). argv[0] is sandbox-exec's own name,
// matching the argv[0] convention execve expects.
func seatbeltRelaunchArgv(profile, exe string, args []string) []string {
	argv := make([]string, 0, len(args)+4)
	argv = append(argv, "sandbox-exec", "-p", profile, "--")
	argv = append(argv, exe)
	argv = append(argv, args...)
	return argv
}
