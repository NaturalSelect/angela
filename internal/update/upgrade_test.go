package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeSource struct {
	releases []Release
	err      error
}

func (f fakeSource) Releases(context.Context) ([]Release, error) { return f.releases, f.err }

// fakeSystem stands in for the host: owner names the package manager that
// claims the executable, and every command run is recorded.
type fakeSystem struct {
	owner    string
	root     bool
	noSudo   bool
	runErr   error
	ran      [][]string
	queried  []string
	commands map[string]bool
}

func (f *fakeSystem) HasCommand(name string) bool {
	if name == "sudo" {
		return !f.noSudo
	}
	return f.commands[name]
}

func (f *fakeSystem) Succeeds(_ context.Context, name string, args ...string) bool {
	f.queried = append(f.queried, name)
	return name == f.owner
}

func (f *fakeSystem) Run(_ context.Context, name string, args ...string) error {
	f.ran = append(f.ran, append([]string{name}, args...))
	return f.runErr
}

func (f *fakeSystem) IsRoot() bool { return f.root }

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}))
		_, err := tw.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	return buf.Bytes()
}

func zipFile(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

// releaseServer serves the given files and returns a release for tag whose
// assets point at them, together with a checksums.txt. A file listed in
// badSum is given a wrong checksum.
func releaseServer(t *testing.T, tag string, files map[string][]byte, badSum ...string) Release {
	t.Helper()
	mux := http.NewServeMux()
	var sums strings.Builder
	rel := Release{TagName: tag}
	for name, data := range files {
		mux.HandleFunc("/"+name, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) })
		sum := sha256.Sum256(data)
		hexSum := hex.EncodeToString(sum[:])
		for _, b := range badSum {
			if b == name {
				hexSum = strings.Repeat("0", 64)
			}
		}
		fmt.Fprintf(&sums, "%s  %s\n", hexSum, name)
		rel.Assets = append(rel.Assets, Asset{Name: name, Size: int64(len(data))})
	}
	mux.HandleFunc("/"+checksumsAsset, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(sums.String())) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	for i := range rel.Assets {
		rel.Assets[i].URL = srv.URL + "/" + rel.Assets[i].Name
	}
	rel.Assets = append(rel.Assets, Asset{Name: checksumsAsset, URL: srv.URL + "/" + checksumsAsset})
	return rel
}

func newTestUpgrader(t *testing.T, current string, platform Platform, sys *fakeSystem, releases ...Release) (*Upgrader, *bytes.Buffer) {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "angela")
	if platform.OS == "windows" {
		exe += ".exe"
	}
	require.NoError(t, os.WriteFile(exe, []byte("old binary"), 0o755))
	var out bytes.Buffer
	return &Upgrader{
		Current:  current,
		Exe:      exe,
		Platform: platform,
		Source:   fakeSource{releases: releases},
		System:   sys,
		Out:      &out,
	}, &out
}

func TestUpgrade_ReplacesBinary(t *testing.T) {
	t.Parallel()

	linux := Platform{"linux", "amd64"}
	rel := releaseServer(t, "v0.0.6", map[string][]byte{
		"angela_0.0.6_Linux_x86_64.tar.gz": tarGz(t, map[string]string{
			"angela_0.0.6_Linux_x86_64/README.md": "readme",
			"angela_0.0.6_Linux_x86_64/angela":    "new binary",
		}),
	})
	u, out := newTestUpgrader(t, "v0.0.5", linux, &fakeSystem{}, rel)

	require.NoError(t, u.Upgrade(t.Context(), false))

	got, err := os.ReadFile(u.Exe)
	require.NoError(t, err)
	require.Equal(t, "new binary", string(got))
	// NOTE: Windows does not store Unix permission bits, so every file reports 0o666.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(u.Exe)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o755), info.Mode().Perm())
	}
	require.Contains(t, out.String(), "Upgrading Angela from v0.0.5 to v0.0.6")
	require.Contains(t, out.String(), "Checksum verified")

	entries, err := os.ReadDir(filepath.Dir(u.Exe))
	require.NoError(t, err)
	require.Len(t, entries, 1, "temporary files must not be left next to the executable")
}

func TestUpgrade_ReplacesBinaryOnWindows(t *testing.T) {
	t.Parallel()

	rel := releaseServer(t, "v0.0.6", map[string][]byte{
		"angela_0.0.6_Windows_x86_64.zip": zipFile(t, map[string]string{
			"angela_0.0.6_Windows_x86_64/angela.exe": "new binary",
		}),
	})
	u, _ := newTestUpgrader(t, "v0.0.5", Platform{"windows", "amd64"}, &fakeSystem{}, rel)

	require.NoError(t, u.Upgrade(t.Context(), false))

	got, err := os.ReadFile(u.Exe)
	require.NoError(t, err)
	require.Equal(t, "new binary", string(got))
	require.NoFileExists(t, u.Exe+".old")
}

func TestUpgrade_ChecksumMismatchKeepsOldBinary(t *testing.T) {
	t.Parallel()

	name := "angela_0.0.6_Linux_x86_64.tar.gz"
	rel := releaseServer(t, "v0.0.6", map[string][]byte{
		name: tarGz(t, map[string]string{"angela_0.0.6_Linux_x86_64/angela": "tampered"}),
	}, name)
	u, _ := newTestUpgrader(t, "v0.0.5", Platform{"linux", "amd64"}, &fakeSystem{}, rel)

	err := u.Upgrade(t.Context(), false)

	require.ErrorContains(t, err, "checksum mismatch")
	got, readErr := os.ReadFile(u.Exe)
	require.NoError(t, readErr)
	require.Equal(t, "old binary", string(got))
}

func TestUpgrade_MissingChecksumsRefused(t *testing.T) {
	t.Parallel()

	rel := releaseServer(t, "v0.0.6", map[string][]byte{
		"angela_0.0.6_Linux_x86_64.tar.gz": tarGz(t, map[string]string{"angela_0.0.6_Linux_x86_64/angela": "new"}),
	})
	rel.Assets = rel.Assets[:len(rel.Assets)-1]
	u, _ := newTestUpgrader(t, "v0.0.5", Platform{"linux", "amd64"}, &fakeSystem{}, rel)

	err := u.Upgrade(t.Context(), false)

	require.ErrorContains(t, err, "cannot be verified")
	got, readErr := os.ReadFile(u.Exe)
	require.NoError(t, readErr)
	require.Equal(t, "old binary", string(got))
}

func TestUpgrade_ArchiveWithoutBinary(t *testing.T) {
	t.Parallel()

	rel := releaseServer(t, "v0.0.6", map[string][]byte{
		"angela_0.0.6_Linux_x86_64.tar.gz": tarGz(t, map[string]string{"angela_0.0.6_Linux_x86_64/README.md": "readme"}),
	})
	u, _ := newTestUpgrader(t, "v0.0.5", Platform{"linux", "amd64"}, &fakeSystem{}, rel)

	err := u.Upgrade(t.Context(), false)

	require.ErrorContains(t, err, "does not contain angela")
	got, readErr := os.ReadFile(u.Exe)
	require.NoError(t, readErr)
	require.Equal(t, "old binary", string(got))
}

func TestUpgrade_NoDowngradeFromPrerelease(t *testing.T) {
	t.Parallel()

	for _, includePrerelease := range []bool{false, true} {
		t.Run(fmt.Sprintf("pre-release=%v", includePrerelease), func(t *testing.T) {
			t.Parallel()
			sys := &fakeSystem{}
			u, out := newTestUpgrader(t, "v0.0.5-rc9", Platform{"linux", "amd64"}, sys,
				Release{TagName: "v0.0.4"}, Release{TagName: "v0.0.4-rc4", Prerelease: true})

			require.NoError(t, u.Upgrade(t.Context(), includePrerelease))

			got, err := os.ReadFile(u.Exe)
			require.NoError(t, err)
			require.Equal(t, "old binary", string(got))
			require.Empty(t, sys.ran)
			require.Contains(t, out.String(), "already up to date")
		})
	}
}

func TestUpgrade_PrereleaseMovesToItsStableRelease(t *testing.T) {
	t.Parallel()

	rel := releaseServer(t, "v0.0.5", map[string][]byte{
		"angela_0.0.5_Linux_x86_64.tar.gz": tarGz(t, map[string]string{"angela_0.0.5_Linux_x86_64/angela": "stable"}),
	})
	u, _ := newTestUpgrader(t, "v0.0.5-rc9", Platform{"linux", "amd64"}, &fakeSystem{}, rel)

	require.NoError(t, u.Upgrade(t.Context(), false))

	got, err := os.ReadFile(u.Exe)
	require.NoError(t, err)
	require.Equal(t, "stable", string(got))
}

func TestUpgrade_HintsAtNewerPrerelease(t *testing.T) {
	t.Parallel()

	u, out := newTestUpgrader(t, "v0.0.5-rc1", Platform{"linux", "amd64"}, &fakeSystem{},
		Release{TagName: "v0.0.5-rc2", Prerelease: true}, Release{TagName: "v0.0.4"})

	require.NoError(t, u.Upgrade(t.Context(), false))

	require.Contains(t, out.String(), "v0.0.5-rc2")
	require.Contains(t, out.String(), "--pre-release")
}

func TestUpgrade_StableIgnoresPrereleaseByDefault(t *testing.T) {
	t.Parallel()

	u, out := newTestUpgrader(t, "v0.0.4", Platform{"linux", "amd64"}, &fakeSystem{},
		Release{TagName: "v0.0.5-rc1", Prerelease: true}, Release{TagName: "v0.0.4"})

	require.NoError(t, u.Upgrade(t.Context(), false))

	require.Contains(t, out.String(), "already up to date")
	require.NotContains(t, out.String(), "--pre-release")
}

func TestUpgrade_DevelopmentBuildRefused(t *testing.T) {
	t.Parallel()

	for _, current := range []string{
		"devel",
		"unknown",
		"",
		"v0.0.5-1-g0770f3000",
		"v0.89.1-0.20261101181851-0770f3000639+dirty",
		"v0.0.0-0.20251231235959-06c807842604",
	} {
		sys := &fakeSystem{}
		u, out := newTestUpgrader(t, current, Platform{"linux", "amd64"}, sys, Release{TagName: "v0.0.5"})

		require.ErrorContains(t, u.Upgrade(t.Context(), false), "development build", current)
		require.Empty(t, sys.ran)
		require.NotContains(t, out.String(), "up to date", current)
	}
}

func TestUpgrade_ListingFailure(t *testing.T) {
	t.Parallel()

	u, _ := newTestUpgrader(t, "v0.0.4", Platform{"linux", "amd64"}, &fakeSystem{})
	u.Source = fakeSource{err: fmt.Errorf("offline")}

	require.ErrorContains(t, u.Upgrade(t.Context(), false), "offline")
}

func TestUpgrade_UsesPackageManager(t *testing.T) {
	t.Parallel()

	tests := []struct {
		owner    string
		arch     string
		asset    string
		wantArgs []string
	}{
		{"dpkg", "amd64", "angela_0.0.6_amd64.deb", []string{"dpkg", "-i"}},
		{"rpm", "arm64", "angela-0.0.6-1.aarch64.rpm", []string{"rpm", "-U", "--oldpackage"}},
		{"apk", "amd64", "angela_0.0.6_x86_64.apk", []string{"apk", "add", "--allow-untrusted"}},
		{"pacman", "amd64", "angela-0.0.6-1-x86_64.pkg.tar.zst", []string{"pacman", "-U", "--noconfirm"}},
	}
	for _, tt := range tests {
		t.Run(tt.owner, func(t *testing.T) {
			t.Parallel()
			rel := releaseServer(t, "v0.0.6", map[string][]byte{tt.asset: []byte("package " + tt.owner)})
			sys := &fakeSystem{owner: tt.owner, root: true}
			u, out := newTestUpgrader(t, "v0.0.5", Platform{"linux", tt.arch}, sys, rel)

			require.NoError(t, u.Upgrade(t.Context(), false))

			require.Len(t, sys.ran, 1)
			cmd := sys.ran[0]
			require.Equal(t, tt.wantArgs, cmd[:len(tt.wantArgs)])
			pkg := cmd[len(cmd)-1]
			require.Equal(t, tt.asset, filepath.Base(pkg))
			got, err := os.ReadFile(u.Exe)
			require.NoError(t, err)
			require.Equal(t, "old binary", string(got), "the package manager owns the executable, it must not be replaced directly")
			require.Contains(t, out.String(), "installed with "+tt.owner)
		})
	}
}

func TestUpgrade_PackageManagerUsesSudoWhenNotRoot(t *testing.T) {
	t.Parallel()

	rel := releaseServer(t, "v0.0.6", map[string][]byte{"angela_0.0.6_amd64.deb": []byte("deb")})
	sys := &fakeSystem{owner: "dpkg"}
	u, _ := newTestUpgrader(t, "v0.0.5", Platform{"linux", "amd64"}, sys, rel)

	require.NoError(t, u.Upgrade(t.Context(), false))

	require.Len(t, sys.ran, 1)
	require.Equal(t, []string{"sudo", "dpkg", "-i"}, sys.ran[0][:3])
}

func TestUpgrade_PackageManagerWithoutSudoOrRoot(t *testing.T) {
	t.Parallel()

	rel := releaseServer(t, "v0.0.6", map[string][]byte{"angela_0.0.6_amd64.deb": []byte("deb")})
	sys := &fakeSystem{owner: "dpkg", noSudo: true}
	u, _ := newTestUpgrader(t, "v0.0.5", Platform{"linux", "amd64"}, sys, rel)

	require.ErrorContains(t, u.Upgrade(t.Context(), false), "as root")
	require.Empty(t, sys.ran)
}

func TestUpgrade_PackageManagerFailure(t *testing.T) {
	t.Parallel()

	rel := releaseServer(t, "v0.0.6", map[string][]byte{"angela_0.0.6_amd64.deb": []byte("deb")})
	sys := &fakeSystem{owner: "dpkg", root: true, runErr: fmt.Errorf("exit status 1")}
	u, out := newTestUpgrader(t, "v0.0.5", Platform{"linux", "amd64"}, sys, rel)

	require.ErrorContains(t, u.Upgrade(t.Context(), false), "dpkg failed")
	require.NotContains(t, out.String(), "is installed")
}

func TestUpgrade_PackageChecksumMismatch(t *testing.T) {
	t.Parallel()

	name := "angela_0.0.6_amd64.deb"
	rel := releaseServer(t, "v0.0.6", map[string][]byte{name: []byte("deb")}, name)
	sys := &fakeSystem{owner: "dpkg", root: true}
	u, _ := newTestUpgrader(t, "v0.0.5", Platform{"linux", "amd64"}, sys, rel)

	require.ErrorContains(t, u.Upgrade(t.Context(), false), "checksum mismatch")
	require.Empty(t, sys.ran, "an unverified package must never reach the package manager")
}

func TestUpgrade_PackageManagersOnlyConsultedOnLinux(t *testing.T) {
	t.Parallel()

	rel := releaseServer(t, "v0.0.6", map[string][]byte{
		"angela_0.0.6_Darwin_arm64.tar.gz": tarGz(t, map[string]string{"angela_0.0.6_Darwin_arm64/angela": "new binary"}),
	})
	sys := &fakeSystem{owner: "dpkg"}
	u, _ := newTestUpgrader(t, "v0.0.5", Platform{"darwin", "arm64"}, sys, rel)

	require.NoError(t, u.Upgrade(t.Context(), false))

	require.Empty(t, sys.queried)
	require.Empty(t, sys.ran)
}
