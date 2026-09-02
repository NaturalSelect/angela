//go:build !windows

package cmd

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetachProcessSetsNewSession(t *testing.T) {
	t.Parallel()

	c := exec.CommandContext(t.Context(), "true")
	detachProcess(c)

	require.NotNil(t, c.SysProcAttr)
	require.True(t, c.SysProcAttr.Setsid)
}

func TestDetachProcessPreservesExistingSysProcAttr(t *testing.T) {
	t.Parallel()

	c := exec.CommandContext(t.Context(), "true")
	c.SysProcAttr = &syscall.SysProcAttr{Chroot: "/tmp"}
	detachProcess(c)

	require.Equal(t, "/tmp", c.SysProcAttr.Chroot, "must not discard a caller-provided SysProcAttr")
	require.True(t, c.SysProcAttr.Setsid)
}
