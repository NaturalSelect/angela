package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/mod/semver"
)

// Upgrader upgrades the running executable to a newer GitHub release.
type Upgrader struct {
	// Current is the version of the running build.
	Current string
	// Exe is the path of the running executable with symlinks resolved.
	Exe      string
	Platform Platform
	Source   Source
	System   System
	Client   *http.Client
	Out      io.Writer
}

// NewUpgrader returns an Upgrader for the running executable.
func NewUpgrader(current string, out io.Writer) (*Upgrader, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("cannot locate the running executable: %w", err)
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return nil, fmt.Errorf("cannot locate the running executable: %w", err)
	}
	return &Upgrader{
		Current:  current,
		Exe:      exe,
		Platform: CurrentPlatform(),
		Source:   DefaultSource,
		System:   hostSystem{},
		Out:      out,
	}, nil
}

// NOTE: This is the output of `git describe --long`, like v0.0.5-1-g0770f3000.
// Such a build is ahead of the tag it names, so it has no release to compare
// with.
var describeSuffix = regexp.MustCompile(`-\d+-g[0-9a-f]{7,}$`)

func isDevelopmentBuild(v string) bool {
	return Info{Current: v}.IsDevelopment() || describeSuffix.MatchString(v) || !semver.IsValid(canonical(v))
}

// Upgrade installs the newest suitable release. It never installs an older
// version than the running one: a pre-release build stays on its line until
// a newer stable release or, with includePrerelease, a newer pre-release is
// published.
//
// If a system package manager owns the executable, the release's package
// file is installed through that package manager. Otherwise the executable
// is replaced in place with the binary from the release archive.
func (u *Upgrader) Upgrade(ctx context.Context, includePrerelease bool) error {
	current := u.Current
	if isDevelopmentBuild(current) {
		return fmt.Errorf("this is a development build of Angela (%s) that cannot be compared with releases, install a release from https://github.com/NaturalSelect/angela/releases instead", current)
	}

	releases, err := u.Source.Releases(ctx)
	if err != nil {
		return fmt.Errorf("failed to list releases: %w", err)
	}
	release := Select(current, releases, includePrerelease)
	if release == nil {
		u.reportUpToDate(current, releases, includePrerelease)
		return nil
	}

	if u.Platform.OS == "windows" {
		os.Remove(u.Exe + ".old")
	}
	fmt.Fprintf(u.Out, "Upgrading Angela from %s to %s.\n", canonical(u.Current), canonical(release.TagName))

	if pm := u.owningPackageManager(ctx); pm != nil {
		err = u.upgradePackage(ctx, *release, *pm)
	} else {
		err = u.upgradeBinary(ctx, *release)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(u.Out, "Angela %s is installed. Restart Angela to use it.\n", canonical(release.TagName))
	return nil
}

func (u *Upgrader) reportUpToDate(current string, releases []Release, includePrerelease bool) {
	fmt.Fprintf(u.Out, "Angela %s is already up to date.\n", canonical(u.Current))
	if !includePrerelease && IsPrerelease(current) {
		if pre := Select(current, releases, true); pre != nil {
			fmt.Fprintf(u.Out, "A newer pre-release, %s, is available. Run `angela upgrade --pre-release` to install it.\n", canonical(pre.TagName))
		}
	}
}

func (u *Upgrader) owningPackageManager(ctx context.Context) *packageManager {
	if u.Platform.OS != "linux" {
		return nil
	}
	for _, pm := range packageManagers {
		if u.System.Succeeds(ctx, pm.name, append(slices.Clone(pm.owner), u.Exe)...) {
			return &pm
		}
	}
	return nil
}

func (u *Upgrader) upgradePackage(ctx context.Context, r Release, pm packageManager) error {
	asset, err := r.PackageAsset(pm.format, u.Platform.Arch)
	if err != nil {
		return err
	}

	var args []string
	if !u.System.IsRoot() {
		if !u.System.HasCommand("sudo") {
			return fmt.Errorf("Angela was installed with %s, which needs root to upgrade: run `angela upgrade` as root", pm.name) //nolint:staticcheck
		}
		args = append(args, "sudo")
	}
	args = append(args, pm.name)
	args = append(args, pm.install...)

	dir, err := os.MkdirTemp("", "angela-upgrade-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	file, err := u.download(ctx, r, asset, dir)
	if err != nil {
		return err
	}
	args = append(args, file)

	fmt.Fprintf(u.Out, "Angela was installed with %s, installing the new package with it: %s\n", pm.name, strings.Join(args, " "))
	if err := u.System.Run(ctx, args[0], args[1:]...); err != nil {
		return fmt.Errorf("%s failed: %w", pm.name, err)
	}
	return nil
}

func (u *Upgrader) upgradeBinary(ctx context.Context, r Release) error {
	name, err := ArchiveName(r.TagName, u.Platform)
	if err != nil {
		return err
	}
	asset, ok := r.FindAsset(name)
	if !ok {
		return fmt.Errorf("release %s has no %s for this platform", r.TagName, name)
	}

	dir, err := os.MkdirTemp("", "angela-upgrade-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	archive, err := u.download(ctx, r, asset, dir)
	if err != nil {
		return err
	}
	if err := u.installBinary(archive); err != nil {
		return err
	}
	fmt.Fprintf(u.Out, "Replaced %s.\n", u.Exe)
	return nil
}
