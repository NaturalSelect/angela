package update

import (
	"cmp"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/mod/semver"
)

func canonical(v string) string {
	return "v" + strings.TrimPrefix(v, "v")
}

func IsPrerelease(v string) bool {
	return semver.Prerelease(canonical(v)) != ""
}

var prereleaseToken = regexp.MustCompile(`\d+|[A-Za-z]+`)

// NOTE: Plain semver compares pre-release identifiers as text, which orders
// rc10 before rc9 and would offer a rollback from rc10 to rc9. Compare runs
// of digits numerically instead.
func compareVersions(a, b string) int {
	a, b = canonical(a), canonical(b)
	if c := semver.Compare(versionCore(a), versionCore(b)); c != 0 {
		return c
	}
	pa, pb := semver.Prerelease(a), semver.Prerelease(b)
	switch {
	case pa == pb:
		return 0
	case pa == "":
		return 1
	case pb == "":
		return -1
	}
	return compareTokens(prereleaseToken.FindAllString(pa, -1), prereleaseToken.FindAllString(pb, -1))
}

func versionCore(v string) string {
	v = strings.TrimSuffix(v, semver.Build(v))
	return strings.TrimSuffix(v, semver.Prerelease(v))
}

func compareTokens(a, b []string) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		na, errA := strconv.ParseUint(a[i], 10, 64)
		nb, errB := strconv.ParseUint(b[i], 10, 64)
		switch {
		case errA == nil && errB == nil:
			if c := cmp.Compare(na, nb); c != 0 {
				return c
			}
		case errA == nil:
			return -1
		case errB == nil:
			return 1
		default:
			if c := strings.Compare(a[i], b[i]); c != 0 {
				return c
			}
		}
	}
	return cmp.Compare(len(a), len(b))
}

// Newest returns the highest-versioned release that may be installed, or
// nil when there is none. Drafts and tags that are not semantic versions
// are skipped, and so are pre-releases unless includePrerelease is set.
func Newest(releases []Release, includePrerelease bool) *Release {
	var best *Release
	for i := range releases {
		r := &releases[i]
		if r.Draft || !semver.IsValid(canonical(r.TagName)) {
			continue
		}
		if !includePrerelease && (r.Prerelease || IsPrerelease(r.TagName)) {
			continue
		}
		if best == nil || compareVersions(r.TagName, best.TagName) > 0 {
			best = r
		}
	}
	return best
}

// Select picks the release an upgrade from current should install. It
// returns nil unless that release is strictly newer than current, so an
// upgrade never moves to an older version and a pre-release build is never
// rolled back to an older stable release.
func Select(current string, releases []Release, includePrerelease bool) *Release {
	best := Newest(releases, includePrerelease)
	if best == nil || compareVersions(best.TagName, current) <= 0 {
		return nil
	}
	return best
}
