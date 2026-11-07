package scratch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Subdirectories of the data directory that hold only files which can be
// produced again. Writers and Sweep share these names so they cannot drift.
const (
	ToolOutputDir = "tool-output"
	WebFetchDir   = "webfetch"
	MCPMediaDir   = "mcp-media"
	ViewerDir     = "viewer"
)

var dirs = []string{ToolOutputDir, WebFetchDir, MCPMediaDir, ViewerDir}

// Result totals what a Sweep removed, or would remove in a dry run.
type Result struct {
	Entries int
	Bytes   int64
}

// Sweep deletes the entries directly under each scratch directory of
// dataDir whose newest modification, anywhere inside the entry, is before
// cutoff. Everything else in dataDir is left alone. With dryRun set it only
// counts.
func Sweep(dataDir string, cutoff time.Time, dryRun bool) (Result, error) {
	var (
		res  Result
		errs []error
	)
	for _, name := range dirs {
		dir := filepath.Join(dataDir, name)

		// NOTE: A symlinked scratch directory is skipped so a sweep can
		// never delete outside the data directory.
		info, err := os.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if !info.IsDir() {
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("listing %s: %w", dir, err))
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					errs = append(errs, err)
				}
				continue
			}

			latest, size, err := inspect(path, info)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if !latest.Before(cutoff) {
				continue
			}
			if !dryRun {
				if err := os.RemoveAll(path); err != nil {
					errs = append(errs, err)
					continue
				}
			}
			res.Entries++
			res.Bytes += size
		}
	}
	return res, errors.Join(errs...)
}

// inspect returns the newest modification time and the total size of the
// file or directory tree at path. Symlinks are not followed.
func inspect(path string, info fs.FileInfo) (time.Time, int64, error) {
	latest := info.ModTime()
	if !info.IsDir() {
		return latest, info.Size(), nil
	}

	var size int64
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		fi, err := d.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if fi.ModTime().After(latest) {
			latest = fi.ModTime()
		}
		if !d.IsDir() {
			size += fi.Size()
		}
		return nil
	})
	return latest, size, err
}
