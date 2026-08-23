package chat

import (
	"encoding/json"
	"path/filepath"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/stringext"
	"github.com/charmbracelet/x/ansi"
)

// toolTargetKeys are the tool input fields, in priority order, that name what
// a tool is acting on.
var toolTargetKeys = []string{"file_path", "path", "command", "pattern", "query", "url", "description"}

// toolTargetIsPath marks the keys holding filesystem paths, which read better
// as a base name on a one-line status.
var toolTargetIsPath = map[string]bool{"file_path": true, "path": true}

// ToolCallTarget extracts a short label for what a tool call acts on — the
// file for Edit, the command for Bash — for one-line status displays. It
// returns "" when the input names no recognizable target.
func ToolCallTarget(tc message.ToolCall, maxWidth int) string {
	if tc.Input == "" {
		return ""
	}

	var input map[string]any
	if err := json.Unmarshal([]byte(tc.Input), &input); err != nil {
		return ""
	}

	for _, key := range toolTargetKeys {
		value, _ := input[key].(string)
		if value == "" {
			continue
		}
		if toolTargetIsPath[key] {
			value = filepath.Base(value)
		}
		value = stringext.NormalizeSpace(value)
		if maxWidth > 0 {
			value = ansi.Truncate(value, maxWidth, "…")
		}
		return value
	}
	return ""
}
