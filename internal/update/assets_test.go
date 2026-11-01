package update

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArchiveName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tag      string
		platform Platform
		want     string
	}{
		{"v0.0.5", Platform{"linux", "amd64"}, "angela_0.0.5_Linux_x86_64.tar.gz"},
		{"v0.0.5", Platform{"linux", "386"}, "angela_0.0.5_Linux_i386.tar.gz"},
		{"v0.0.5", Platform{"linux", "arm"}, "angela_0.0.5_Linux_armv7.tar.gz"},
		{"v0.0.5", Platform{"linux", "arm64"}, "angela_0.0.5_Linux_arm64.tar.gz"},
		{"v0.0.5", Platform{"darwin", "arm64"}, "angela_0.0.5_Darwin_arm64.tar.gz"},
		{"v0.0.5", Platform{"windows", "amd64"}, "angela_0.0.5_Windows_x86_64.zip"},
		{"v0.0.5", Platform{"freebsd", "amd64"}, "angela_0.0.5_Freebsd_x86_64.tar.gz"},
		{"v0.0.5", Platform{"android", "arm64"}, "angela_0.0.5_Android_arm64.tar.gz"},
		{"v0.0.5-rc9", Platform{"linux", "amd64"}, "angela_0.0.5-rc9_Linux_x86_64.tar.gz"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			got, err := ArchiveName(tt.tag, tt.platform)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}

	_, err := ArchiveName("v0.0.5", Platform{"plan9", "amd64"})
	require.ErrorContains(t, err, "operating system")
	_, err = ArchiveName("v0.0.5", Platform{"linux", "riscv64"})
	require.ErrorContains(t, err, "architecture")
}

func TestPackageAsset(t *testing.T) {
	t.Parallel()

	stable := Release{TagName: "v0.0.5", Assets: assets(
		"angela-0.0.5-1-x86_64.pkg.tar.zst",
		"angela-0.0.5-1-aarch64.pkg.tar.zst",
		"angela-0.0.5-1.x86_64.rpm",
		"angela-0.0.5-1.aarch64.rpm",
		"angela_0.0.5_amd64.deb",
		"angela_0.0.5_arm64.deb",
		"angela_0.0.5_x86_64.apk",
		"angela_0.0.5_aarch64.apk",
		"angela_0.0.5_Linux_x86_64.tar.gz",
	)}
	// Release candidates spell the version differently in every format.
	rc := Release{TagName: "v0.0.5-rc9", Assets: assets(
		"angela-0.0.5rc9-1-x86_64.pkg.tar.zst",
		"angela-0.0.5.rc9-1.x86_64.rpm",
		"angela_0.0.5.rc9_amd64.deb",
		"angela_0.0.5_rc9_x86_64.apk",
	)}

	tests := []struct {
		name    string
		release Release
		format  string
		arch    string
		want    string
	}{
		{"deb amd64", stable, "deb", "amd64", "angela_0.0.5_amd64.deb"},
		{"deb arm64", stable, "deb", "arm64", "angela_0.0.5_arm64.deb"},
		{"rpm amd64", stable, "rpm", "amd64", "angela-0.0.5-1.x86_64.rpm"},
		{"rpm arm64", stable, "rpm", "arm64", "angela-0.0.5-1.aarch64.rpm"},
		{"apk amd64", stable, "apk", "amd64", "angela_0.0.5_x86_64.apk"},
		{"pacman amd64", stable, "pacman", "amd64", "angela-0.0.5-1-x86_64.pkg.tar.zst"},
		{"pacman arm64", stable, "pacman", "arm64", "angela-0.0.5-1-aarch64.pkg.tar.zst"},
		{"rc deb", rc, "deb", "amd64", "angela_0.0.5.rc9_amd64.deb"},
		{"rc rpm", rc, "rpm", "amd64", "angela-0.0.5.rc9-1.x86_64.rpm"},
		{"rc apk", rc, "apk", "amd64", "angela_0.0.5_rc9_x86_64.apk"},
		{"rc pacman", rc, "pacman", "amd64", "angela-0.0.5rc9-1-x86_64.pkg.tar.zst"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := tt.release.PackageAsset(tt.format, tt.arch)
			require.NoError(t, err)
			require.Equal(t, tt.want, got.Name)
		})
	}

	_, err := stable.PackageAsset("deb", "386")
	require.ErrorContains(t, err, "no deb package")
	_, err = stable.PackageAsset("deb", "riscv64")
	require.ErrorContains(t, err, "architecture")
	_, err = stable.PackageAsset("snap", "amd64")
	require.ErrorContains(t, err, "unsupported package format")
}

func TestParseChecksums(t *testing.T) {
	t.Parallel()

	got := parseChecksums("ABC123  angela_0.0.5_Linux_x86_64.tar.gz\n\nnot a checksum line\ndef456 *angela.deb\n")
	require.Equal(t, map[string]string{
		"angela_0.0.5_Linux_x86_64.tar.gz": "abc123",
		"angela.deb":                       "def456",
	}, got)
}

func assets(names ...string) []Asset {
	out := make([]Asset, 0, len(names))
	for _, n := range names {
		out = append(out, Asset{Name: n})
	}
	return out
}
