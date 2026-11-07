package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/db"
	"github.com/NaturalSelect/angela/internal/scratch"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func newVacuumTestCmd(t *testing.T, dataDir string, dryRun bool) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.Flags().String("data-dir", "", "")
	cmd.Flags().Bool("debug", false, "")
	cmd.Flags().Bool("dry-run", false, "")
	require.NoError(t, cmd.Flags().Set("data-dir", dataDir))
	if dryRun {
		require.NoError(t, cmd.Flags().Set("dry-run", "true"))
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	return cmd, &out
}

// seedBloatedDatabase leaves dataDir with a database whose file is far larger
// than its content, as it would be after a long-lived session was deleted.
func seedBloatedDatabase(t *testing.T, dataDir string) {
	t.Helper()
	ctx := t.Context()
	db.ResetPool()
	t.Cleanup(db.ResetPool)
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	q := db.New(conn)
	_, err = q.CreateSession(ctx, db.CreateSessionParams{ID: "gone", Title: "gone"})
	require.NoError(t, err)
	_, err = q.CreateGeneratedImage(ctx, db.CreateGeneratedImageParams{
		ID: "img_1", SessionID: "gone", Prompt: "p", Provider: "p", Model: "m",
		MimeType: "image/png", Data: make([]byte, 4<<20),
	})
	require.NoError(t, err)
	require.NoError(t, q.DeleteSession(ctx, "gone"))
	require.NoError(t, db.Release(dataDir))
}

func seedScratchFile(t *testing.T, dataDir, rel string, age time.Duration) string {
	t.Helper()
	path := filepath.Join(dataDir, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("spilled output"), 0o600))
	when := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(path, when, when))
	require.NoError(t, os.Chtimes(filepath.Dir(path), when, when))
	return path
}

func dbFileSize(t *testing.T, dataDir string) int64 {
	t.Helper()
	info, err := os.Stat(filepath.Join(dataDir, "angela.db"))
	require.NoError(t, err)
	return info.Size()
}

func TestRunVacuum_DeletesOldScratchFilesAndShrinksTheDatabase(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	seedBloatedDatabase(t, dataDir)
	before := dbFileSize(t, dataDir)
	require.Greater(t, before, int64(4<<20))

	stale := seedScratchFile(t, dataDir, filepath.Join(scratch.ToolOutputDir, "s1", "output-1.txt"), 2*time.Hour)
	fresh := seedScratchFile(t, dataDir, filepath.Join(scratch.ToolOutputDir, "s2", "output-1.txt"), 10*time.Minute)
	config := filepath.Join(dataDir, "angela.json")
	require.NoError(t, os.WriteFile(config, []byte("{}"), 0o600))
	require.NoError(t, os.Chtimes(config, time.Now().Add(-72*time.Hour), time.Now().Add(-72*time.Hour)))

	cmd, out := newVacuumTestCmd(t, dataDir, false)
	require.NoError(t, runVacuum(cmd, nil))

	require.NoFileExists(t, stale)
	require.FileExists(t, fresh, "anything modified within the last hour is protected")
	require.FileExists(t, config)
	require.Less(t, dbFileSize(t, dataDir), int64(1<<20))
	require.Contains(t, out.String(), "Deleted 1 temporary entry")
	require.Contains(t, out.String(), "Vacuumed the database")
}

func TestRunVacuum_DryRunChangesNothing(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	seedBloatedDatabase(t, dataDir)
	before := dbFileSize(t, dataDir)
	stale := seedScratchFile(t, dataDir, filepath.Join(scratch.WebFetchDir, "s1", "page-1.md"), 2*time.Hour)

	cmd, out := newVacuumTestCmd(t, dataDir, true)
	require.NoError(t, runVacuum(cmd, nil))

	require.FileExists(t, stale)
	require.Equal(t, before, dbFileSize(t, dataDir), "a dry run must not compact the database")
	require.Contains(t, out.String(), "Would delete 1 temporary entry")
	require.NotContains(t, out.String(), "Vacuumed")
}

func TestRunVacuum_WithoutADatabaseStillSweepsAndCreatesNone(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	stale := seedScratchFile(t, dataDir, filepath.Join(scratch.MCPMediaDir, "shot.png"), 2*time.Hour)

	cmd, out := newVacuumTestCmd(t, dataDir, false)
	require.NoError(t, runVacuum(cmd, nil))

	require.NoFileExists(t, stale)
	require.NoFileExists(t, filepath.Join(dataDir, "angela.db"), "vacuum must not create a database")
	require.Contains(t, out.String(), "No database to vacuum")
}
