package update

import (
	"context"
	"os"
	"os/exec"
)

// System is the part of the host that the upgrader reaches outside the file
// system, so tests can stand in for package managers.
type System interface {
	// HasCommand reports whether name is an executable on PATH.
	HasCommand(name string) bool
	// Succeeds runs name quietly and reports whether it exited with status 0.
	Succeeds(ctx context.Context, name string, args ...string) bool
	// Run runs name attached to the user's terminal, so that sudo can ask
	// for a password.
	Run(ctx context.Context, name string, args ...string) error
	// IsRoot reports whether the process has root privileges.
	IsRoot() bool
}

type hostSystem struct{}

func (hostSystem) HasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (h hostSystem) Succeeds(ctx context.Context, name string, args ...string) bool {
	if !h.HasCommand(name) {
		return false
	}
	return exec.CommandContext(ctx, name, args...).Run() == nil
}

func (hostSystem) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (hostSystem) IsRoot() bool {
	return os.Geteuid() == 0
}

// packageManager describes how to ask a system package manager who owns a
// file and how to install a package file that was downloaded from a
// release. Both argument lists get a path appended.
type packageManager struct {
	name    string
	format  string
	owner   []string
	install []string
}

// NOTE: Releases only attach package files, they are not published to a
// package repository, so upgrading through a package manager means
// installing the downloaded file. rpm needs --oldpackage because it orders
// 0.0.5.rc9 after 0.0.5 and would refuse to move from a release candidate to
// the stable release.
var packageManagers = []packageManager{
	{name: "dpkg", format: "deb", owner: []string{"-S"}, install: []string{"-i"}},
	{name: "rpm", format: "rpm", owner: []string{"-qf"}, install: []string{"-U", "--oldpackage"}},
	{name: "apk", format: "apk", owner: []string{"info", "-W"}, install: []string{"add", "--allow-untrusted"}},
	{name: "pacman", format: "pacman", owner: []string{"-Qo"}, install: []string{"-U", "--noconfirm"}},
}
