package sandbox

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// setSandboxStateForTest overrides the process-global sandbox state and
// restores it on cleanup. Tests using it must not be parallel.
func setSandboxStateForTest(t *testing.T, enteredNow, blockNetwork bool, rs *ruleSet) {
	t.Helper()

	origEntered := entered.Swap(enteredNow)
	origNetwork := restrictChildNetwork.Swap(blockNetwork)
	origRules := appliedRules.Swap(rs)
	t.Cleanup(func() {
		entered.Store(origEntered)
		restrictChildNetwork.Store(origNetwork)
		appliedRules.Store(origRules)
	})
}

func TestCurrentStatus_NotEntered(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}
	setSandboxStateForTest(t, false, false, nil)

	st := CurrentStatus(true)
	require.Equal(t, BackendLandlock, st.Backend)
	require.False(t, st.Active)
	require.False(t, st.FilesystemRestricted)
	require.Empty(t, st.ReadDirs)
	require.Empty(t, st.WriteDirs)
	require.False(t, st.BlocksChildNetwork)
}

func TestCurrentStatus_Entered(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}
	rs := resolve(Config{
		ReadWrite:      []string{"/work"},
		ReadOnly:       []string{"/"},
		ReadWriteFiles: []string{"/work/out.log"},
		ReadOnlyFiles:  []string{"/etc/hosts"},
	})
	setSandboxStateForTest(t, true, true, &rs)

	st := CurrentStatus(true)
	require.Equal(t, BackendLandlock, st.Backend)
	require.True(t, st.Active)
	require.True(t, st.FilesystemRestricted)
	require.Equal(t, []string{"/", "/work"}, st.ReadDirs)
	require.Equal(t, []string{"/work"}, st.WriteDirs)
	require.Equal(t, []string{"/etc/hosts", "/work/out.log"}, st.ReadFiles)
	require.Equal(t, []string{"/work/out.log"}, st.WriteFiles)
	require.True(t, st.BlocksChildNetwork)
}

func TestCurrentStatus_HidesDevGrants(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}
	rs := resolve(Config{ReadOnly: []string{"/"}})
	require.Contains(t, rs.writeFiles, "/dev/null")
	setSandboxStateForTest(t, true, false, &rs)

	st := CurrentStatus(true)
	require.Empty(t, st.ReadFiles)
	require.Empty(t, st.WriteFiles)
}

func TestCurrentStatus_EmptyConfigRestrictsNothing(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}
	rs := resolve(Config{AllowNetwork: true})
	setSandboxStateForTest(t, true, false, &rs)

	st := CurrentStatus(true)
	require.True(t, st.Active)
	require.False(t, st.FilesystemRestricted)
	require.False(t, st.BlocksChildNetwork)
}

func TestCurrentStatus_Docker(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}

	orig := InDocker
	InDocker = func() bool { return true }
	t.Cleanup(func() { InDocker = orig })

	st := CurrentStatus(false)
	require.Equal(t, BackendDocker, st.Backend)
	require.True(t, st.Active)
	require.Empty(t, st.WriteDirs)
}

func TestCurrentStatus_DockerOverriddenByNoDockerSandbox(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}

	orig := InDocker
	InDocker = func() bool { return true }
	t.Cleanup(func() { InDocker = orig })
	setSandboxStateForTest(t, false, false, nil)

	st := CurrentStatus(true)
	require.Equal(t, BackendLandlock, st.Backend)
	require.False(t, st.Active)
}

func TestCurrentStatus_LandlockABI(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Skipping test on non-Linux")
	}
	setSandboxStateForTest(t, false, false, nil)

	require.GreaterOrEqual(t, CurrentStatus(true).LandlockABI, 0)
}

func TestWithoutPaths(t *testing.T) {
	t.Parallel()

	require.Nil(t, withoutPaths(nil, safeDevReadFiles))
	require.Nil(t, withoutPaths(safeDevReadFiles, safeDevReadFiles))
	require.Equal(
		t,
		[]string{"/a"},
		withoutPaths([]string{"/a", "/dev/null"}, safeDevWriteFiles),
	)
}
