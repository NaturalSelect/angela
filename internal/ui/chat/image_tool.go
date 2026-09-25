package chat

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg" // register JPEG format for image.Decode
	_ "image/png"  // register PNG format for image.Decode

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/message"
	fimage "github.com/NaturalSelect/angela/internal/ui/image"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/ui/util"
)

// -----------------------------------------------------------------------------
// Image Tools (ImageGenerate / ImageEdit)
// -----------------------------------------------------------------------------

// ImageCaps is the subset of terminal capabilities an [ImageItem] needs to
// decide whether and how to render an inline preview.
type ImageCaps struct {
	// Kitty is whether the terminal supports the Kitty graphics protocol;
	// without it an image tool call only ever shows the plain-text
	// fallback.
	Kitty bool
	// Tmux is whether the session is running inside tmux, whose escape
	// sequences need passthrough wrapping.
	Tmux bool
	// Cell is the terminal's per-cell pixel size, used to size the image
	// to an integral number of cells.
	Cell fimage.CellSize
}

// ImageReadyMsg reports that the image identified by ItemID has been
// transmitted to the terminal at the grid size Key (a "colsxrows"
// string), so the item's next render can draw Kitty placeholders for it.
type ImageReadyMsg struct {
	ItemID string
	Key    string
}

// ImageItem is a [ToolMessageItem] that renders an inline image over the
// Kitty graphics protocol once it has been transmitted.
type ImageItem interface {
	ToolMessageItem

	// SetImageCaps updates the terminal capabilities this item renders
	// against.
	SetImageCaps(caps ImageCaps)
	// ImageTransmitCmd returns a command that (re)transmits the image to
	// the terminal if the grid size it would render at for width is not
	// already transmitted or already in flight, or nil if nothing needs
	// to happen.
	ImageTransmitCmd(width int) tea.Cmd
	// SetImageReady marks a grid size ("colsxrows") as transmitted.
	SetImageReady(key string)
}

// Grid sizing for the inline image preview. cols is capped well below
// maxTextWidth so a generated image never dominates the transcript the
// way a wide terminal would otherwise allow; rows is clamped to a range
// that always shows something without letting a very tall or very wide
// source image blow out the scrollback.
const (
	imageGridMaxCols = 64
	imageGridMinRows = 1
	imageGridMaxRows = 20
	// imageFallbackCellWidth and imageFallbackCellHeight stand in for the
	// terminal's real cell size when it is not yet known (the zero value
	// before the first pixel-size query resolves), so sizing never
	// divides by zero.
	imageFallbackCellWidth  = 10
	imageFallbackCellHeight = 20
)

// ImageToolMessageItem is a message item that represents an
// ImageGenerate or ImageEdit tool call, rendering the generated image
// inline once it has been transmitted to the terminal.
type ImageToolMessageItem struct {
	*baseToolMessageItem

	caps ImageCaps

	// pendingKey is the "colsxrows" grid size currently being
	// transmitted, if any.
	pendingKey string
	// transmittedKey is the "colsxrows" grid size already on the
	// terminal, if any. RenderTool only draws Kitty placeholders when
	// this matches the size it would render at now; otherwise it falls
	// back to a plain-text summary.
	transmittedKey string
}

var _ ImageItem = (*ImageToolMessageItem)(nil)

// NewImageToolMessageItem creates a new [ImageToolMessageItem].
func NewImageToolMessageItem(
	sty *styles.Styles,
	toolCall message.ToolCall,
	result *message.ToolResult,
	canceled bool,
) *ImageToolMessageItem {
	t := &ImageToolMessageItem{}
	t.baseToolMessageItem = newBaseToolMessageItem(sty, toolCall, result, &ImageToolRenderContext{item: t}, canceled)
	return t
}

// SetImageCaps implements [ImageItem]. A change can flip whether Kitty
// placeholders are even an option and can shift the grid size images
// target, so any cached render is dropped.
func (t *ImageToolMessageItem) SetImageCaps(caps ImageCaps) {
	if t.caps == caps {
		return
	}
	t.caps = caps
	t.clearCache()
	t.Bump()
}

// SetImageReady implements [ImageItem].
func (t *ImageToolMessageItem) SetImageReady(key string) {
	if t.transmittedKey == key {
		return
	}
	t.transmittedKey = key
	if t.pendingKey == key {
		t.pendingKey = ""
	}
	t.clearCache()
	t.Bump()
}

// ImageTransmitCmd implements [ImageItem].
//
// width is capped twice to land on the same grid size RenderTool will
// ask for: RawRender caps the raw list width once before calling
// RenderTool (every image tool call has hasCappedWidth set), and
// RenderTool caps again like every other tool renderer in this file.
func (t *ImageToolMessageItem) ImageTransmitCmd(width int) tea.Cmd {
	if !t.caps.Kitty {
		return nil
	}
	if t.result == nil || t.result.IsError || t.result.Data == "" {
		return nil
	}

	cappedWidth := cappedMessageWidth(cappedMessageWidth(width))
	cols, rows := t.gridSize(cappedWidth, t.result)
	key := gridKey(cols, rows)
	if key == t.transmittedKey || key == t.pendingKey {
		return nil
	}
	t.pendingKey = key

	id := t.ID()
	data := t.result.Data
	cs := t.caps.Cell
	tmux := t.caps.Tmux

	return tea.Sequence(
		func() tea.Msg {
			raw, err := base64.StdEncoding.DecodeString(data)
			if err != nil {
				return util.NewErrorMsg(fmt.Errorf("failed to decode generated image: %w", err))
			}
			img, _, err := image.Decode(bytes.NewReader(raw))
			if err != nil {
				return util.NewErrorMsg(fmt.Errorf("failed to decode generated image: %w", err))
			}
			return fimage.KittyTransmit(id, img, cs, cols, rows, tmux)()
		},
		func() tea.Msg {
			return ImageReadyMsg{ItemID: id, Key: key}
		},
	)
}

// gridSize computes the cols x rows Kitty grid this image should render
// at for the given width, from the image's own aspect ratio in result's
// metadata.
func (t *ImageToolMessageItem) gridSize(width int, result *message.ToolResult) (cols, rows int) {
	var meta tools.ImageResponseMetadata
	_ = json.Unmarshal([]byte(result.Metadata), &meta)

	cols = max(1, min(width-toolBodyLeftPaddingTotal, imageGridMaxCols))

	cellW, cellH := t.caps.Cell.Width, t.caps.Cell.Height
	if cellW <= 0 || cellH <= 0 {
		cellW, cellH = imageFallbackCellWidth, imageFallbackCellHeight
	}
	imgW, imgH := meta.Width, meta.Height
	if imgW <= 0 || imgH <= 0 {
		imgW, imgH = 1, 1
	}

	rows = cols * cellW * imgH / (imgW * cellH)
	rows = max(imageGridMinRows, min(imageGridMaxRows, rows))
	return cols, rows
}

// gridKey formats a grid size as the key used to track transmitted and
// pending sizes.
func gridKey(cols, rows int) string {
	return fmt.Sprintf("%dx%d", cols, rows)
}

// renderBody draws the tool's successful output: the Kitty placeholder
// grid once the image has been transmitted at the current size, or a
// plain-text summary otherwise (no Kitty support, or a (re)transmit
// still in flight for this size).
func (t *ImageToolMessageItem) renderBody(sty *styles.Styles, result *message.ToolResult, width int) string {
	if result == nil || result.Data == "" {
		return ""
	}

	cols, rows := t.gridSize(width, result)
	key := gridKey(cols, rows)

	if t.caps.Kitty && t.transmittedKey == key {
		return sty.Tool.Body.Render(fimage.KittyPlaceholders(t.ID(), cols, rows))
	}

	return toolOutputImageContent(sty, result.Data, result.MIMEType)
}

// imageToolHeaderParams captures the one field ImageGenerateParams and
// ImageEditParams share that the header needs: the prompt driving the
// call. Unmarshaling into this instead of either tool's own params type
// lets one render context serve both tool names.
type imageToolHeaderParams struct {
	Prompt string `json:"prompt"`
}

// ImageToolRenderContext renders ImageGenerate/ImageEdit tool messages.
type ImageToolRenderContext struct {
	item *ImageToolMessageItem
}

// RenderTool implements the [ToolRenderer] interface.
func (r *ImageToolRenderContext) RenderTool(sty *styles.Styles, width int, opts *ToolRenderOpts) string {
	cappedWidth := cappedMessageWidth(width)
	if opts.IsPending() {
		return pendingTool(sty, opts.ToolCall.Name, opts.Anim, opts.Compact)
	}

	var params imageToolHeaderParams
	_ = json.Unmarshal([]byte(opts.ToolCall.Input), &params)

	header := toolHeader(sty, opts.Status, opts.ToolCall.Name, cappedWidth, opts, params.Prompt)
	if opts.Compact {
		return header
	}

	if earlyState, ok := toolEarlyStateContent(sty, opts, cappedWidth); ok {
		return joinToolParts(header, earlyState)
	}

	if !opts.HasResult() {
		return header
	}

	body := r.item.renderBody(sty, opts.Result, cappedWidth)
	if body == "" {
		return header
	}
	return joinToolParts(header, body)
}
