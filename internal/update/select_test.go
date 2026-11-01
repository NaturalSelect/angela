package update

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func rel(tag string, prerelease bool) Release {
	return Release{TagName: tag, Prerelease: prerelease}
}

func TestSelect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		current    string
		releases   []Release
		prerelease bool
		want       string
	}{
		{
			name:     "stable to newer stable",
			current:  "0.0.4",
			releases: []Release{rel("v0.0.5", false), rel("v0.0.4", false)},
			want:     "v0.0.5",
		},
		{
			name:     "already on the latest stable",
			current:  "0.0.5",
			releases: []Release{rel("v0.0.5", false), rel("v0.0.4", false)},
			want:     "",
		},
		{
			name:     "stable ignores a newer pre-release by default",
			current:  "0.0.4",
			releases: []Release{rel("v0.0.5-rc1", true), rel("v0.0.4", false)},
			want:     "",
		},
		{
			name:       "stable takes a newer pre-release with the flag",
			current:    "0.0.4",
			releases:   []Release{rel("v0.0.5-rc1", true), rel("v0.0.4", false)},
			prerelease: true,
			want:       "v0.0.5-rc1",
		},
		{
			name:     "pre-release is not rolled back to an older stable",
			current:  "0.0.5-rc9",
			releases: []Release{rel("v0.0.4", false), rel("v0.0.3", false)},
			want:     "",
		},
		{
			name:       "pre-release is not rolled back with the flag either",
			current:    "0.0.5-rc9",
			releases:   []Release{rel("v0.0.4", false), rel("v0.0.4-rc4", true)},
			prerelease: true,
			want:       "",
		},
		{
			name:     "pre-release moves to its stable release",
			current:  "0.0.5-rc9",
			releases: []Release{rel("v0.0.5", false), rel("v0.0.5-rc9", true)},
			want:     "v0.0.5",
		},
		{
			name:     "pre-release stays put when only newer pre-releases exist",
			current:  "0.0.5-rc1",
			releases: []Release{rel("v0.0.5-rc2", true), rel("v0.0.4", false)},
			want:     "",
		},
		{
			name:       "pre-release moves to a newer pre-release with the flag",
			current:    "0.0.5-rc1",
			releases:   []Release{rel("v0.0.5-rc2", true), rel("v0.0.4", false)},
			prerelease: true,
			want:       "v0.0.5-rc2",
		},
		{
			name:       "rc10 is newer than rc9",
			current:    "0.0.5-rc9",
			releases:   []Release{rel("v0.0.5-rc10", true), rel("v0.0.5-rc9", true)},
			prerelease: true,
			want:       "v0.0.5-rc10",
		},
		{
			name:       "rc9 is not an upgrade from rc10",
			current:    "0.0.5-rc10",
			releases:   []Release{rel("v0.0.5-rc9", true)},
			prerelease: true,
			want:       "",
		},
		{
			name:       "a stable release beats the pre-releases of the same version",
			current:    "0.0.5-rc9",
			releases:   []Release{rel("v0.0.5-rc10", true), rel("v0.0.5", false)},
			prerelease: true,
			want:       "v0.0.5",
		},
		{
			name:     "drafts are skipped",
			current:  "0.0.4",
			releases: []Release{{TagName: "v0.0.6", Draft: true}, rel("v0.0.5", false)},
			want:     "v0.0.5",
		},
		{
			name:     "tags that are not versions are skipped",
			current:  "0.0.4",
			releases: []Release{rel("nightly", false), rel("v0.0.5", false)},
			want:     "v0.0.5",
		},
		{
			name:     "a pre-release tag is skipped even if GitHub does not flag it",
			current:  "0.0.4",
			releases: []Release{rel("v0.0.5-rc1", false), rel("v0.0.4", false)},
			want:     "",
		},
		{
			name:    "no releases",
			current: "0.0.4",
			want:    "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Select(tt.current, tt.releases, tt.prerelease)
			if tt.want == "" {
				require.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			require.Equal(t, tt.want, got.TagName)
		})
	}
}

func TestCompareVersions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"v1.2.3", "1.2.3", 0},
		{"1.2.4", "1.2.3", 1},
		{"1.10.0", "1.9.0", 1},
		{"1.2.3", "1.2.3-rc1", 1},
		{"1.2.3-rc1", "1.2.3", -1},
		{"1.2.3-rc2", "1.2.3-rc1", 1},
		{"1.2.3-rc10", "1.2.3-rc9", 1},
		{"1.2.3-rc-4", "1.2.3-rc3", 1},
		{"1.2.3-alpha", "1.2.3-beta", -1},
		{"1.2.3-beta.2", "1.2.3-beta.11", -1},
		{"1.2.4-rc1", "1.2.3", 1},
		{"1.2.3+build5", "1.2.3", 0},
	}
	for _, tt := range tests {
		t.Run(tt.a+" vs "+tt.b, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, compareVersions(tt.a, tt.b))
			require.Equal(t, -tt.want, compareVersions(tt.b, tt.a))
		})
	}
}

func TestIsPrerelease(t *testing.T) {
	t.Parallel()

	require.True(t, IsPrerelease("v0.0.5-rc1"))
	require.True(t, IsPrerelease("0.0.5-rc-4"))
	require.False(t, IsPrerelease("v0.0.5"))
	require.False(t, IsPrerelease("0.0.5"))
}
