//go:build !windows

package cmd

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddSignalsAppendsSigterm(t *testing.T) {
	t.Parallel()

	got := addSignals([]os.Signal{os.Interrupt})

	require.Equal(t, []os.Signal{os.Interrupt, syscall.SIGTERM}, got)
}

func TestAddSignalsOnEmptyInput(t *testing.T) {
	t.Parallel()

	got := addSignals(nil)

	require.Equal(t, []os.Signal{syscall.SIGTERM}, got)
}
