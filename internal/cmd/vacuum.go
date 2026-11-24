package cmd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/db"
	"github.com/NaturalSelect/angela/internal/scratch"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

const (
	// vacuumMinAge protects anything a running session may still be using.
	vacuumMinAge = time.Hour

	defaultTrimSessionDays = 30

	// NOTE: The cap keeps the cutoff arithmetic far from overflow, where a
	// huge value could wrap into a cutoff in the future and match everything.
	maxTrimSessionDays = 36500
)

var vacuumCmd = &cobra.Command{
	Use:     "vacuum",
	Aliases: []string{"gc"},
	Short:   "Compact the database and delete old temporary files",
	Long: `Compact the project database with SQLite VACUUM and delete temporary
files Angela saved under the data directory: oversized tool output, large
fetched pages, MCP media and files written for an external viewer.

Nothing modified in the last hour is deleted. Sessions, messages and
configuration are never touched unless you pass --trim-session, which also
deletes the sessions that have not been used for a while (30 days by
default). A session counts as used when it, or any subagent or branch
session under it, changed within that time. Deleting a session removes its
messages, files and generated images and cannot be undone. Give another
number of days as --trim-session=N, with an equals sign.

Run it while no other Angela is using the project, because VACUUM needs the
database to itself.`,
	Example: `
# Clean up the current project
angela vacuum

# Show what would be deleted
angela vacuum --dry-run

# Also delete sessions not used for 30 days
angela vacuum --trim-session

# Also delete sessions not used for 90 days, previewing first
angela vacuum --trim-session=90 --dry-run
  `,
	Args: cobra.NoArgs,
	RunE: runVacuum,
}

func init() {
	vacuumCmd.Flags().Bool("dry-run", false, "Report what would be deleted without changing anything")
	vacuumCmd.Flags().Int("trim-session", 0, "Also delete sessions not used for N days (30 if N is omitted, give it as --trim-session=N)")
	vacuumCmd.Flags().Lookup("trim-session").NoOptDefVal = strconv.Itoa(defaultTrimSessionDays)
}

func runVacuum(cmd *cobra.Command, _ []string) error {
	trimDays, err := trimSessionDays(cmd)
	if err != nil {
		return err
	}
	cwd, err := ResolveCwd(cmd)
	if err != nil {
		return err
	}
	dataDir, _ := cmd.Flags().GetString("data-dir")
	debug, _ := cmd.Flags().GetBool("debug")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	cfg, err := config.Init(cwd, dataDir, debug)
	if err != nil {
		return fmt.Errorf("failed to initialize config: %w", err)
	}
	dataDir = cfg.Config().Options.DataDirectory
	out := cmd.OutOrStdout()

	swept, sweepErr := scratch.Sweep(dataDir, time.Now().Add(-vacuumMinAge), dryRun)
	verb := "Deleted"
	if dryRun {
		verb = "Would delete"
	}
	fmt.Fprintf(out, "%s %d temporary %s, freeing %s.\n",
		verb, swept.Entries, plural(swept.Entries, "entry", "entries"), humanize.Bytes(uint64(swept.Bytes)))

	dbPath := filepath.Join(dataDir, "angela.db")
	before, err := databaseSize(dbPath)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(out, "No database to vacuum.")
		return sweepErr
	}
	if err != nil {
		return errors.Join(sweepErr, err)
	}
	if dryRun {
		if trimDays > 0 {
			idle, err := idleSessionsReadOnly(cmd.Context(), dbPath, trimDays)
			if err != nil {
				return errors.Join(sweepErr, err)
			}
			fmt.Fprintln(out, trimSummary(verb, len(idle), trimDays))
		}
		fmt.Fprintf(out, "The database is %s. A vacuum would compact it.\n", humanize.Bytes(uint64(before)))
		return sweepErr
	}

	if err := vacuumDatabase(cmd.Context(), out, dataDir, trimDays); err != nil {
		return errors.Join(sweepErr, err)
	}
	after, err := databaseSize(dbPath)
	if err != nil {
		return errors.Join(sweepErr, err)
	}
	fmt.Fprintf(out, "Vacuumed the database: %s to %s.\n", humanize.Bytes(uint64(before)), humanize.Bytes(uint64(after)))
	return sweepErr
}

// trimSessionDays is the idle age from --trim-session, or 0 when the flag
// was not given.
func trimSessionDays(cmd *cobra.Command) (int, error) {
	if !cmd.Flags().Changed("trim-session") {
		return 0, nil
	}
	days, err := cmd.Flags().GetInt("trim-session")
	if err != nil {
		return 0, err
	}
	if days < 1 || days > maxTrimSessionDays {
		return 0, fmt.Errorf("invalid --trim-session value %d: use a number of days from 1 to %d", days, maxTrimSessionDays)
	}
	return days, nil
}

func trimSummary(verb string, sessions, days int) string {
	return fmt.Sprintf("%s %d %s not used in the last %d %s.",
		verb, sessions, plural(sessions, "session", "sessions"), days, plural(days, "day", "days"))
}

// vacuumDatabase deletes the sessions idle for trimDays (none when it is 0)
// and then compacts the database, so the space they held is given back.
func vacuumDatabase(ctx context.Context, out io.Writer, dataDir string, trimDays int) (err error) {
	conn, err := db.Connect(ctx, dataDir)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer func() {
		if releaseErr := db.Release(dataDir); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
	}()

	// NOTE: A failed trim still lets the vacuum run, like a failed scratch
	// sweep does, so one stubborn session does not block the rest.
	var trimErr error
	if trimDays > 0 {
		var deleted int
		deleted, trimErr = trimSessions(ctx, conn, trimDays)
		fmt.Fprintln(out, trimSummary("Deleted", deleted, trimDays))
	}
	return errors.Join(trimErr, db.Vacuum(ctx, conn))
}

// trimSessions deletes every session tree not used for days and returns how
// many top-level sessions it removed. It keeps going after a failed delete.
func trimSessions(ctx context.Context, conn *sql.DB, days int) (deleted int, err error) {
	q := db.New(conn)
	idle, err := idleSessions(ctx, q, days)
	if err != nil {
		return 0, err
	}
	sessions := session.NewService(q, conn)
	for _, id := range idle {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return deleted, errors.Join(err, ctxErr)
		}
		if delErr := sessions.Delete(ctx, id); delErr != nil {
			err = errors.Join(err, fmt.Errorf("failed to delete session %s: %w", id, delErr))
			continue
		}
		deleted++
	}
	return deleted, err
}

// idleSessionsReadOnly finds what trimSessions would delete. It opens the
// database read-only, so a dry run neither migrates nor writes anything.
func idleSessionsReadOnly(ctx context.Context, dbPath string, days int) ([]string, error) {
	conn, err := db.ConnectReadOnly(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	defer conn.Close()
	return idleSessions(ctx, db.New(conn), days)
}

// idleSessions returns the top-level sessions whose whole tree, subagent and
// branch sessions included, was last updated more than days ago. Deleting a
// top-level session removes its tree, so a recently used child keeps its
// root alive.
func idleSessions(ctx context.Context, q *db.Queries, days int) ([]string, error) {
	rows, err := q.ListSessionActivity(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	cutoff := time.Now().AddDate(0, 0, -days).Unix()

	children := make(map[string][]db.ListSessionActivityRow, len(rows))
	var roots []db.ListSessionActivityRow
	for _, row := range rows {
		if row.ParentSessionID.Valid {
			children[row.ParentSessionID.String] = append(children[row.ParentSessionID.String], row)
			continue
		}
		roots = append(roots, row)
	}

	var idle []string
	for _, root := range roots {
		inUse := false
		for stack := []db.ListSessionActivityRow{root}; len(stack) > 0 && !inUse; {
			cur := stack[len(stack)-1]
			stack = append(stack[:len(stack)-1], children[cur.ID]...)
			inUse = cur.UpdatedAt >= cutoff
		}
		if !inUse {
			idle = append(idle, root.ID)
		}
	}
	return idle, nil
}

// databaseSize is the on-disk size of the database and its write-ahead log.
// It wraps os.ErrNotExist when there is no database file.
func databaseSize(dbPath string) (int64, error) {
	info, err := os.Stat(dbPath)
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if wal, err := os.Stat(dbPath + "-wal"); err == nil {
		size += wal.Size()
	}
	return size, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
