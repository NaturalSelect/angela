package db

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/version"
)

func TestAcquireDataDirLock_WritesOwnerInfo(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()

	l, err := acquireDataDirLock(dataDir)
	require.NoError(t, err)
	require.NotNil(t, l)
	t.Cleanup(l.release)

	lockPath := filepath.Join(dataDir, dataDirLockFile)
	raw, err := os.ReadFile(lockPath)
	require.NoError(t, err)

	var info dataDirOwnerInfo
	require.NoError(t, json.Unmarshal(raw, &info))
	require.Equal(t, os.Getpid(), info.PID)
	require.Equal(t, version.Version, info.Version)

	_, err = time.Parse(time.RFC3339, info.StartedAt)
	require.NoError(t, err, "started_at should be RFC3339")
}

func TestAcquireDataDirLock_Contended(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()

	first, err := acquireDataDirLock(dataDir)
	require.NoError(t, err)
	t.Cleanup(first.release)

	second, err := acquireDataDirLock(dataDir)
	require.Nil(t, second)
	require.ErrorIs(t, err, ErrDataDirLocked)
	require.Contains(t, err.Error(), fmt.Sprintf("pid=%d", os.Getpid()))
}

func TestAcquireDataDirLock_ReleaseAllowsReacquire(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()

	first, err := acquireDataDirLock(dataDir)
	require.NoError(t, err)
	first.release()

	second, err := acquireDataDirLock(dataDir)
	require.NoError(t, err)
	require.NotNil(t, second)
	t.Cleanup(second.release)
}

// TestAcquireDataDirLock_SkipEnvReturnsNoopLock exercises the internal
// early-return path directly: Connect gates the call to
// acquireDataDirLock on skipDataDirLock() itself, so this branch is
// otherwise unreachable through the public API.
func TestAcquireDataDirLock_SkipEnvReturnsNoopLock(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("ANGELA_SKIP_DATADIR_LOCK", "true")

	first, err := acquireDataDirLock(dataDir)
	require.NoError(t, err)
	require.NotNil(t, first)

	lockPath := filepath.Join(dataDir, dataDirLockFile)
	_, statErr := os.Stat(lockPath)
	require.True(t, os.IsNotExist(statErr), "no-op lock must not write a lock file")

	second, err := acquireDataDirLock(dataDir)
	require.NoError(t, err, "no-op lock must never contend")
	require.NotNil(t, second)

	require.NotPanics(t, first.release)
	require.NotPanics(t, second.release)
}

// TestAcquireDataDirLock_FailsWhenLockFileCannotBeOpened covers the
// non-contended lock.TryFile failure branch: dataDir is itself a
// regular file, so the lock file path can't be created underneath it.
func TestAcquireDataDirLock_FailsWhenLockFileCannotBeOpened(t *testing.T) {
	t.Parallel()

	dataDir := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(dataDir, []byte("x"), 0o600))

	l, err := acquireDataDirLock(dataDir)
	require.Nil(t, l)
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrDataDirLocked)
	require.Contains(t, err.Error(), "failed to lock data directory")
}

func TestSkipDataDirLock(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"unset", "", false},
		{"false", "false", false},
		{"zero", "0", false},
		{"unparseable", "yes", false},
		{"true", "true", true},
		{"one", "1", true},
		{"upper true", "TRUE", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ANGELA_SKIP_DATADIR_LOCK", tt.value)
			require.Equal(t, tt.want, skipDataDirLock())
		})
	}
}

func TestReadOwnerInfo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		create  bool
		content []byte
		want    dataDirOwnerInfo
	}{
		{name: "missing file", create: false, want: dataDirOwnerInfo{}},
		{name: "empty file", create: true, content: []byte{}, want: dataDirOwnerInfo{}},
		{name: "malformed json", create: true, content: []byte("{not valid json"), want: dataDirOwnerInfo{}},
		{
			name:    "valid json",
			create:  true,
			content: []byte(`{"pid":123,"version":"1.0.0","started_at":"2024-01-01T00:00:00Z"}`),
			want:    dataDirOwnerInfo{PID: 123, Version: "1.0.0", StartedAt: "2024-01-01T00:00:00Z"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := filepath.Join(dir, dataDirLockFile)
			if tt.create {
				require.NoError(t, os.WriteFile(path, tt.content, 0o600))
			}

			require.Equal(t, tt.want, readOwnerInfo(path))
		})
	}
}

func TestContendedLockError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		create  bool
		content []byte
		want    []string
		unwant  []string
	}{
		{
			name:   "no owner info",
			create: false,
			unwant: []string{"pid="},
		},
		{
			name:    "pid only",
			create:  true,
			content: []byte(`{"pid":4242}`),
			want:    []string{"pid=4242"},
			unwant:  []string{"started_at"},
		},
		{
			name:    "full owner info",
			create:  true,
			content: []byte(`{"pid":4242,"version":"1.2.3","started_at":"2024-01-01T00:00:00Z"}`),
			want:    []string{"pid=4242", "version=1.2.3", "started_at=2024-01-01T00:00:00Z"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dataDir := t.TempDir()
			lockPath := filepath.Join(dataDir, dataDirLockFile)
			if tt.create {
				require.NoError(t, os.WriteFile(lockPath, tt.content, 0o600))
			}

			err := contendedLockError(dataDir, lockPath)
			require.ErrorIs(t, err, ErrDataDirLocked)
			for _, w := range tt.want {
				require.Contains(t, err.Error(), w)
			}
			for _, uw := range tt.unwant {
				require.NotContains(t, err.Error(), uw)
			}
		})
	}
}

func TestWriteOwnerInfo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, dataDirLockFile)

	require.NoError(t, writeOwnerInfo(path))

	info := readOwnerInfo(path)
	require.Equal(t, os.Getpid(), info.PID)
	require.Equal(t, version.Version, info.Version)
	require.NotEmpty(t, info.StartedAt)
}

func TestWriteOwnerInfo_Error(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing-subdir", "lockfile")
	require.Error(t, writeOwnerInfo(path))
}
