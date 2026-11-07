package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/db"
	"github.com/NaturalSelect/angela/internal/scratch"
	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
)

// vacuumMinAge protects anything a running session may still be using.
const vacuumMinAge = time.Hour

var vacuumCmd = &cobra.Command{
	Use:     "vacuum",
	Aliases: []string{"gc"},
	Short:   "Compact the database and delete old temporary files",
	Long: `Compact the project database with SQLite VACUUM and delete temporary
files Angela saved under the data directory: oversized tool output, large
fetched pages, MCP media and files written for an external viewer.

Nothing modified in the last hour is deleted. Sessions, messages and
configuration are never touched. Run it while no other Angela is using the
project, because VACUUM needs the database to itself.`,
	Example: `
# Clean up the current project
angela vacuum

# Show what would be deleted
angela vacuum --dry-run
  `,
	Args: cobra.NoArgs,
	RunE: runVacuum,
}

func init() {
	vacuumCmd.Flags().Bool("dry-run", false, "Report what would be deleted without changing anything")
}

func runVacuum(cmd *cobra.Command, _ []string) error {
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
		fmt.Fprintf(out, "The database is %s. A vacuum would compact it.\n", humanize.Bytes(uint64(before)))
		return sweepErr
	}

	if err := vacuumDatabase(cmd.Context(), dataDir); err != nil {
		return errors.Join(sweepErr, err)
	}
	after, err := databaseSize(dbPath)
	if err != nil {
		return errors.Join(sweepErr, err)
	}
	fmt.Fprintf(out, "Vacuumed the database: %s to %s.\n", humanize.Bytes(uint64(before)), humanize.Bytes(uint64(after)))
	return sweepErr
}

func vacuumDatabase(ctx context.Context, dataDir string) (err error) {
	conn, err := db.Connect(ctx, dataDir)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer func() {
		if releaseErr := db.Release(dataDir); releaseErr != nil {
			err = errors.Join(err, releaseErr)
		}
	}()
	return db.Vacuum(ctx, conn)
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
