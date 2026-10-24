package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/NaturalSelect/angela/internal/profiling"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/dialog"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// exportProfile writes a runtime profile of this process to disk. The data
// comes straight from the runtime, not from the workspace. The result is
// reported as a toast, and a timed profile shows an animated one while it
// records, since a frozen toast would read as stuck.
func exportProfile(action dialog.ActionExportProfile) tea.Cmd {
	profile := action.Profile
	record := func() tea.Msg {
		// NOTE: Unlike the other exports this cannot be cancelled by the
		// user, as nothing in the UI owns a context for it. A timed profile
		// is bounded by the seconds they asked for.
		ctx := context.Background()

		workingDir, err := os.Getwd()
		if err != nil {
			return util.ReportError(err)()
		}

		defaultName := profile.FileName(os.Getpid(), time.Now(), profile.Extension())
		target := chat.ResolveExportTarget(action.Output, defaultName, workingDir)
		outPath, err := chat.UniqueExportPath(target)
		if err != nil {
			return util.ReportError(err)()
		}
		if err := profiling.CaptureToFile(ctx, outPath, profile, action.Seconds); err != nil {
			return util.ReportError(fmt.Errorf("failed to export %s profile: %w", profile.Name, err))()
		}
		return util.ReportInfo(fmt.Sprintf("Exported %s profile to %s", profile.Name, filepath.Clean(outPath)))()
	}

	if !profile.Timed {
		return record
	}
	return tea.Batch(
		util.CmdHandler(util.InfoMsg{
			Type:     util.InfoTypeInfo,
			Msg:      fmt.Sprintf("Recording %s profile for %ds", profile.Name, action.Seconds),
			Animated: true,
		}),
		record,
	)
}
