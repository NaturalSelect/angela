package version

import (
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeriveBuildID(t *testing.T) {
	t.Parallel()

	id := deriveBuildID()
	require.NotEmpty(t, id)
	require.NotEqual(t, "unknown", id)

	exe, err := os.Executable()
	require.NoError(t, err)
	fi, err := os.Stat(exe)
	require.NoError(t, err)
	want := strconv.FormatInt(fi.ModTime().UnixNano(), 36)
	require.Equal(t, want, id)
}

func TestPackageDefaults(t *testing.T) {
	t.Parallel()

	// init() always sets these from build info or fallback defaults, so
	// they must never be empty once the package is loaded.
	require.NotEmpty(t, Version)
	require.NotEmpty(t, Commit)
	require.NotEmpty(t, BuildID)
}
