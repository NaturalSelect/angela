package model

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/NaturalSelect/angela/internal/commands"
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

// handleExportProfile handles dialog.ActionExportProfile.
func (m *UI) handleExportProfile(msg dialog.ActionExportProfile) tea.Cmd {
	var cmds []tea.Cmd
	if msg.Profile.Name == "" {
		m.dialog.CloseDialog(dialog.CommandsID)
		cmds = append(cmds, func() tea.Msg {
			// Profiles describe this process, so they are written
			// through its own filesystem, never the workspace's.
			workingDir, err := os.Getwd()
			if err != nil {
				return util.ReportError(err)()
			}
			return exportProfilesLoadedMsg{workingDir: workingDir}
		})
		return tea.Batch(cmds...)
	}
	m.dialog.CloseDialog(dialog.ArgumentsID)
	cmds = append(cmds, exportProfile(msg))
	return tea.Batch(cmds...)
}

// handleSelectExportProfile handles dialog.ActionSelectExportProfile.
func (m *UI) handleSelectExportProfile(msg dialog.ActionSelectExportProfile) tea.Cmd {
	m.dialog.CloseDialog(dialog.ProfilesID)
	profile := msg.Profile
	defaultName := profile.FileName(os.Getpid(), time.Now(), profile.Extension())
	arguments := []commands.Argument{{ID: "OUTPUT", Title: "Output Path", Required: true}}
	description := "Writes a snapshot of this process to a file for `go tool pprof`."
	if profile.Timed {
		arguments = append(arguments, commands.Argument{ID: "SECONDS", Title: "Seconds to Record", Required: true})
		description = "Records this process for a while, then writes the result to a file."
	}
	argsDialog := dialog.NewArguments(
		m.com,
		"Export Profile: "+profile.Name,
		description,
		arguments,
		dialog.ActionExportProfile{Profile: profile},
	)
	argsDialog.SetValue("OUTPUT", filepath.Join(msg.WorkingDir, defaultName))
	if profile.Timed {
		argsDialog.SetValue("SECONDS", strconv.Itoa(profiling.DefaultSeconds))
	}
	m.dialog.OpenDialog(argsDialog)
	return nil
}
