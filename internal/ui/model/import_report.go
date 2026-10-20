package model

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agenttools "github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/home"
	"github.com/NaturalSelect/angela/internal/ui/chat"
	"github.com/NaturalSelect/angela/internal/ui/common"
)

// readReportFile reads the report file at path from this process's own
// filesystem. A relative path is taken from the working directory and a
// leading "~" from the home directory.
//
// NOTE: The workspace's working directory is never used here. In
// client-server mode it names a path on the daemon host, while the user
// typed a path on the machine they are sitting at.
func readReportFile(path string) (agenttools.Report, error) {
	path = home.Long(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		workingDir, err := os.Getwd()
		if err != nil {
			return agenttools.Report{}, err
		}
		path = filepath.Join(workingDir, path)
	}

	info, err := os.Stat(path)
	if err != nil {
		return agenttools.Report{}, err
	}
	if info.IsDir() {
		return agenttools.Report{}, fmt.Errorf("%s is a directory, not a report file", path)
	}
	if info.Size() > common.MaxAttachmentSize {
		return agenttools.Report{}, fmt.Errorf("report file is too large (%d bytes, the limit is %d)", info.Size(), common.MaxAttachmentSize)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return agenttools.Report{}, err
	}
	return chat.ReportFromMarkdown(data, path), nil
}

// importReportSummary tells the user what an import did and which id the
// agent can load the report by.
func importReportSummary(result agenttools.ImportResult) string {
	id := result.Report.ID
	switch {
	case result.AlreadyPresent:
		return fmt.Sprintf("Report %s is already in this session", id)
	case result.RequestedID != "" && result.RequestedID != id:
		return fmt.Sprintf("Imported as %s (%s is taken by a different report)", id, result.RequestedID)
	default:
		return "Imported report " + id
	}
}
