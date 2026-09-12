package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
)

// privatePrefixes lists macOS top-level directories that are actually
// symlinks into /private. The kernel resolves a path before matching
// it against an SBPL rule, so a rule written against one spelling
// (e.g. "/tmp/x") never matches the path the kernel actually checks
// once resolved ("/private/tmp/x"), and a caller-supplied path might
// arrive in either form. seatbeltPathForms emits both.
var privatePrefixes = []string{"/tmp", "/var", "/etc"}

// SeatbeltSandbox restricts the process using macOS's Seatbelt
// mechanism, applied to the running process in place via
// sandbox_init(3) (see applySeatbeltProfile in seatbelt_darwin.go):
// unlike the deprecated sandbox-exec(1) command line tool, this needs
// no relaunch, so it behaves exactly like LandlockSandbox — callable
// at any time, restricting the calling process directly. It's still
// irreversible for the life of the process.
type SeatbeltSandbox struct{}

// IsInSandbox reports whether EnterSandbox has already restricted
// this process, on either backend; see sandbox.go's entered.
func (SeatbeltSandbox) IsInSandbox() bool {
	return entered.Load()
}

// EnterSandbox applies cfg to the current process via
// applySeatbeltProfile. A second call, on either backend, is a no-op;
// see sandbox.go's entered doc.
//
// Seatbelt has no way to restrict network access for only this
// process's children the way Landlock's restrictChildNetwork side
// channel does: doing so would mean applying a second, tighter
// profile to a child from inside this already-sandboxed process,
// which the kernel rejects outright. So unlike LandlockSandbox,
// cfg.AllowNetwork false here fails closed with ErrNotSupported
// instead of silently leaving children's network access open.
func (SeatbeltSandbox) EnterSandbox(cfg Config) error {
	if entered.Load() {
		return nil
	}
	if !cfg.AllowNetwork {
		return fmt.Errorf("enter sandbox: blocking outbound network for spawned commands is not supported on macOS, since an already-sandboxed process cannot apply a second, tighter profile to its own children (drop --sandbox-no-network): %w", ErrNotSupported)
	}

	profile, err := seatbeltProfile(resolve(cfg).existing())
	if err != nil {
		return fmt.Errorf("enter sandbox: build profile: %w", err)
	}
	if err := applySeatbeltProfile(profile); err != nil {
		return fmt.Errorf("enter sandbox: apply seatbelt profile: %w", err)
	}

	entered.Store(true)
	return nil
}

// seatbeltProfile renders rs as an SBPL (Sandbox Profile Language)
// document for applySeatbeltProfile.
//
// Mirrors LandlockSandbox.EnterSandbox: an empty rs renders as the
// fully permissive "(allow default)" profile instead of a
// deny-everything one, so entering an empty sandbox still marks
// IsInSandbox true without restricting anything, matching Landlock's
// own no-op-when-empty behavior. rs.readFiles/writeFiles get
// "literal" rules rather than the "subpath" rules rs.readDirs/
// writeDirs use, so a single-file grant can't be tricked into
// covering every other file in its parent directory.
func seatbeltProfile(rs ruleSet) (string, error) {
	var b strings.Builder
	b.WriteString("(version 1)\n(allow default)\n")

	if rs.empty() {
		return b.String(), nil
	}

	b.WriteString("(deny file-read* file-write*)\n")
	b.WriteString("(allow file-read-metadata)\n")

	if err := writePathRule(&b, "file-read*", "subpath", rs.readDirs); err != nil {
		return "", err
	}
	if err := writePathRule(&b, "file-write*", "subpath", rs.writeDirs); err != nil {
		return "", err
	}
	if err := writePathRule(&b, "file-read*", "literal", rs.readFiles); err != nil {
		return "", err
	}
	if err := writePathRule(&b, "file-write*", "literal", rs.writeFiles); err != nil {
		return "", err
	}

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
