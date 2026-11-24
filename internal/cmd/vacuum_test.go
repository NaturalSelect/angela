package cmd

import (
	"bytes"
	"database/sql"
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
	cmd.Flags().Int("trim-session", 0, "")
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

type agedSession struct {
	id     string
	parent string
	age    time.Duration
}

// seedAgedSessions stores each session with one message and backdates its
// last update by age, as if it had sat unused that long.
func seedAgedSessions(t *testing.T, dataDir string, sessions ...agedSession) {
	t.Helper()
	ctx := t.Context()
	db.ResetPool()
	t.Cleanup(db.ResetPool)
	conn, err := db.Connect(ctx, dataDir)
	require.NoError(t, err)
	q := db.New(conn)
	for _, s := range sessions {
		_, err = q.CreateSession(ctx, db.CreateSessionParams{
			ID:              s.id,
			ParentSessionID: sql.NullString{String: s.parent, Valid: s.parent != ""},
			Title:           s.id,
		})
		require.NoError(t, err)
		_, err = q.CreateMessage(ctx, db.CreateMessageParams{ID: s.id + "-msg", SessionID: s.id, Role: "user", Parts: "[]"})
		require.NoError(t, err)
	}
	// NOTE: The trigger stamps updated_at on every UPDATE, so it has to go
	// before the sessions can be backdated.
	_, err = conn.ExecContext(ctx, "DROP TRIGGER update_sessions_updated_at")
	require.NoError(t, err)
	for _, s := range sessions {
		_, err = conn.ExecContext(ctx, "UPDATE sessions SET updated_at = ? WHERE id = ?", time.Now().Add(-s.age).Unix(), s.id)
		require.NoError(t, err)
	}
	require.NoError(t, db.Release(dataDir))
}

// openStoredDatabase opens dataDir's database for the test to inspect what a
// vacuum left behind. The connection is closed when the test ends.
func openStoredDatabase(t *testing.T, dataDir string) *db.Queries {
	t.Helper()
	db.ResetPool()
	t.Cleanup(db.ResetPool)
	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)
	return db.New(conn)
}

// storedSessionIDs lists every session left in dataDir's database, subagent
// sessions included.
func storedSessionIDs(t *testing.T, dataDir string) []string {
	t.Helper()
	rows, err := openStoredDatabase(t, dataDir).ListSessionActivity(t.Context())
	require.NoError(t, err)
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}

const oneDay = 24 * time.Hour

func TestRunVacuum_TrimSessionDeletesOnlySessionTreesUnusedForTheWholePeriod(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	seedAgedSessions(t, dataDir,
		agedSession{id: "stale", age: 45 * oneDay},
		agedSession{id: "stale-child", parent: "stale", age: 45 * oneDay},
		agedSession{id: "stale-grandchild", parent: "stale-child", age: 45 * oneDay},
		agedSession{id: "busy-child-root", age: 45 * oneDay},
		agedSession{id: "busy-child", parent: "busy-child-root", age: 40 * oneDay},
		agedSession{id: "busy-grandchild", parent: "busy-child", age: 2 * oneDay},
		agedSession{id: "recent", age: 29 * oneDay},
	)

	cmd, out := newVacuumTestCmd(t, dataDir, false)
	require.NoError(t, cmd.Flags().Set("trim-session", "30"))
	require.NoError(t, runVacuum(cmd, nil))

	require.ElementsMatch(t,
		[]string{"busy-child-root", "busy-child", "busy-grandchild", "recent"},
		storedSessionIDs(t, dataDir))
	require.Contains(t, out.String(), "Deleted 1 session not used in the last 30 days.")
	require.Contains(t, out.String(), "Vacuumed the database")

	messages, err := openStoredDatabase(t, dataDir).ListMessagesBySession(t.Context(), "stale-grandchild")
	require.NoError(t, err)
	require.Empty(t, messages, "the messages of a deleted session go with it")
}

func TestRunVacuum_TrimSessionDryRunDeletesNothing(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	seedAgedSessions(t, dataDir,
		agedSession{id: "stale", age: 45 * oneDay},
		agedSession{id: "stale-child", parent: "stale", age: 45 * oneDay},
		agedSession{id: "recent", age: oneDay},
	)

	cmd, out := newVacuumTestCmd(t, dataDir, true)
	require.NoError(t, cmd.Flags().Set("trim-session", "30"))
	require.NoError(t, runVacuum(cmd, nil))

	require.ElementsMatch(t, []string{"stale", "stale-child", "recent"}, storedSessionIDs(t, dataDir))
	require.Contains(t, out.String(), "Would delete 1 session not used in the last 30 days.")
	require.NotContains(t, out.String(), "Vacuumed")
}

func TestRunVacuum_WithoutTrimSessionKeepsOldSessions(t *testing.T) {
	isolateSessionEnv(t)
	dataDir := t.TempDir()
	seedAgedSessions(t, dataDir, agedSession{id: "stale", age: 400 * oneDay})

	cmd, out := newVacuumTestCmd(t, dataDir, false)
	require.NoError(t, runVacuum(cmd, nil))

	require.Equal(t, []string{"stale"}, storedSessionIDs(t, dataDir))
	require.NotContains(t, out.String(), "session")
}

func TestRunVacuum_TrimSessionRejectsDaysOutOfRangeBeforeTouchingAnything(t *testing.T) {
	for _, days := range []string{"0", "-5", "36501"} {
		t.Run(days, func(t *testing.T) {
			isolateSessionEnv(t)
			dataDir := t.TempDir()
			seedAgedSessions(t, dataDir, agedSession{id: "stale", age: 400 * oneDay})
			stale := seedScratchFile(t, dataDir, filepath.Join(scratch.ToolOutputDir, "s1", "output-1.txt"), 2*time.Hour)

			cmd, out := newVacuumTestCmd(t, dataDir, false)
			require.NoError(t, cmd.Flags().Set("trim-session", days))
			err := runVacuum(cmd, nil)

			require.ErrorContains(t, err, "invalid --trim-session value "+days+": use a number of days from 1 to 36500")
			require.Equal(t, []string{"stale"}, storedSessionIDs(t, dataDir))
			require.FileExists(t, stale)
			require.Empty(t, out.String())
		})
	}
}

func TestVacuumCmd_TrimSessionDefaultsToThirtyDaysWithoutAValue(t *testing.T) {
	flag := vacuumCmd.Flags().Lookup("trim-session")
	require.NotNil(t, flag)
	require.Equal(t, "30", flag.NoOptDefVal)
	require.Equal(t, "0", flag.DefValue, "trimming is off unless the flag is given")
}
