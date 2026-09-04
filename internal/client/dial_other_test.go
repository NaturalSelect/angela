//go:build !windows

package client

import (
	"context"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDialPipeContextUnsupportedOnNonWindows(t *testing.T) {
	t.Parallel()

	_, err := dialPipeContext(context.Background(), "whatever")
	require.ErrorIs(t, err, syscall.EAFNOSUPPORT)
}
