//go:build !windows

package shell

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/sandbox"
	"github.com/stretchr/testify/require"
)

// TestNetworkRestrictedArgv_NoRestriction verifies that when the
// shell tool isn't asked to restrict outbound network access,
// networkRestrictedArgv passes path and args through unchanged.
func TestNetworkRestrictedArgv_NoRestriction(t *testing.T) {
	t.Parallel()

	path, args, err := networkRestrictedArgv("/bin/true", []string{"true"})
	require.NoError(t, err)
	require.Equal(t, "/bin/true", path)
	require.Equal(t, []string{"true"}, args)
}

// TestNetworkRestrictedArgv_WrapsWhenRestricted verifies that once
// sandbox.ShouldRestrictChildNetwork reports true,
// networkRestrictedArgv rewrites path and args exactly the way
// sandbox.WrapForChildNetworkRestriction does, rather than starting a
// real process to observe the effect. Not parallel: it flips the
// package-level sandbox flag via the test-only seam, which would
// otherwise race with any other test in this package spawning a real
// command through Run.
func TestNetworkRestrictedArgv_WrapsWhenRestricted(t *testing.T) {
	restore := sandbox.SetRestrictChildNetworkForTest(true)
	defer restore()

	path, args, err := networkRestrictedArgv("/bin/true", []string{"true"})
	require.NoError(t, err)

	want, wrapErr := sandbox.WrapForChildNetworkRestriction("/bin/true", []string{"true"})
	require.NoError(t, wrapErr)
	require.Equal(t, want[0], path)
	require.Equal(t, want, args)
}
