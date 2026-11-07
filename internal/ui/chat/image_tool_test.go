package chat

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/NaturalSelect/angela/internal/ui/styles"
)

func renderImageToolWithoutKitty(t *testing.T, metadata string) string {
	t.Helper()
	sty := styles.CharmtonePantera()

	toolCall := message.ToolCall{ID: "i1", Name: toolnames.ImageGenerate, Input: `{"prompt":"a red fox"}`, Finished: true}
	result := &message.ToolResult{
		ToolCallID: "i1",
		Data:       "aGVsbG8=",
		MIMEType:   "image/png",
		Metadata:   metadata,
	}
	item := NewImageToolMessageItem(&sty, toolCall, result, false)

	if expandable, ok := ToolMessageItem(item).(Expandable); ok {
		expandable.ToggleExpanded()
	}
	return ansi.Strip(item.Render(100))
}

func TestImageToolFallbackShowsImageIDAndExportHint(t *testing.T) {
	t.Parallel()

	out := renderImageToolWithoutKitty(t, `{"image_id":"img_1a2b3c4d5e6f","width":1024,"height":1024}`)
	require.Contains(t, out, "img_1a2b3c4d5e6f")
	require.Contains(t, out, "1024x1024")
	require.Contains(t, out, "Export Image")
	require.NotContains(t, out, "Loaded Image")
}

func TestImageToolFallbackWithoutMetadataUsesGenericLine(t *testing.T) {
	t.Parallel()

	out := renderImageToolWithoutKitty(t, "")
	require.Contains(t, out, "Loaded Image")
	require.Contains(t, out, "image/png")
}
