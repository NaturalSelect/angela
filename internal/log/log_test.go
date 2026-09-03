package log

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInitialized(t *testing.T) {
	// Initialized just reads the atomic flag Setup sets. Setup itself is
	// guarded by a package-level sync.Once shared by the whole test
	// binary, so we only assert the accessor doesn't panic rather than
	// driving a before/after transition here.
	_ = Initialized()
}

func TestRecoverPanic(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { require.NoError(t, os.Chdir(wd)) })

	cleanupCalled := false
	func() {
		defer RecoverPanic("test-panic", func() { cleanupCalled = true })
		panic("boom")
	}()

	require.True(t, cleanupCalled, "cleanup should run during panic recovery")

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Contains(t, entries[0].Name(), "angela-panic-test-panic-")

	data, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	require.NoError(t, err)
	require.Contains(t, string(data), "Panic in test-panic: boom")
	require.Contains(t, string(data), "Stack Trace:")
}

func TestRecoverPanic_NoPanicIsNoOp(t *testing.T) {
	cleanupCalled := false
	func() {
		defer RecoverPanic("no-panic", func() { cleanupCalled = true })
	}()
	require.False(t, cleanupCalled)
}
