package image

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"image"
	"image/color"
	"io"
	"log/slog"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/disintegration/imaging"
	paintbrush "github.com/jordanella/go-ansi-paintbrush"
)

// TransmittedMsg is a message indicating that an image has been transmitted to
// the terminal.
type TransmittedMsg struct {
	ID string
}

// Encoding represents the encoding format of the image.
type Encoding byte

// Image encodings.
const (
	EncodingBlocks Encoding = iota
	EncodingKitty
)

type imageKey struct {
	id   string
	cols int
	rows int
}

// Hash returns a hash value for the image key.
// This uses FNV-32a for simplicity and speed.
func (k imageKey) Hash() uint32 {
	h := fnv.New32a()
	_, _ = io.WriteString(h, k.ID())
	return h.Sum32()
}

// ID returns a unique string representation of the image key.
func (k imageKey) ID() string {
	return fmt.Sprintf("%s-%dx%d", k.id, k.cols, k.rows)
}

// CellSize represents the size of a single terminal cell in pixels.
type CellSize struct {
	Width, Height int
}

type cachedImage struct {
	img        image.Image
	cols, rows int
}

var (
	cachedImages = map[imageKey]cachedImage{}
	cachedMutex  sync.RWMutex
)

// ResetCache clears the image cache, freeing all cached decoded images.
func ResetCache() {
	cachedMutex.Lock()
	clear(cachedImages)
	cachedMutex.Unlock()
}

// fitImage resizes the image to fit within the specified dimensions in
// terminal cells, maintaining the aspect ratio.
func fitImage(id string, img image.Image, cs CellSize, cols, rows int) image.Image {
	if img == nil {
		return nil
	}

	key := imageKey{id: id, cols: cols, rows: rows}

	cachedMutex.RLock()
	cached, ok := cachedImages[key]
	cachedMutex.RUnlock()
	if ok {
		return cached.img
	}

	if cs.Width == 0 || cs.Height == 0 {
		return img
	}

	maxWidth := cols * cs.Width
	maxHeight := rows * cs.Height

	img = imaging.Fit(img, maxWidth, maxHeight, imaging.Lanczos)

	cachedMutex.Lock()
	cachedImages[key] = cachedImage{
		img:  img,
		cols: cols,
		rows: rows,
	}
	cachedMutex.Unlock()

	return img
}

// HasTransmitted checks if the image with the given ID has already been
// transmitted to the terminal.
func HasTransmitted(id string, cols, rows int) bool {
	key := imageKey{id: id, cols: cols, rows: rows}

	cachedMutex.RLock()
	_, ok := cachedImages[key]
	cachedMutex.RUnlock()
	return ok
}

// Transmit transmits the image data to the terminal if needed. This is used to
// cache the image on the terminal for later rendering.
func (e Encoding) Transmit(id string, img image.Image, cs CellSize, cols, rows int, tmux bool) tea.Cmd {
	if img == nil {
		return nil
	}

	key := imageKey{id: id, cols: cols, rows: rows}

	cachedMutex.RLock()
	_, ok := cachedImages[key]
	cachedMutex.RUnlock()
	if ok {
		return nil
	}

	if e != EncodingKitty {
		return func() tea.Msg {
			cachedMutex.Lock()
			cachedImages[key] = cachedImage{
				img:  img,
				cols: cols,
				rows: rows,
			}
			cachedMutex.Unlock()
			return TransmittedMsg{ID: key.ID()}
		}
	}

	return func() tea.Msg {
		// fitImage both resizes to fit cols x rows and caches the
		// result under key, which is what makes the cache checks above
		// (and Render/HasTransmitted for the same key) see this image
		// as transmitted from now on. KittyTransmit gets the
		// already-fit image with a zero CellSize so it skips resizing
		// it a second time.
		fitted := fitImage(id, img, cs, cols, rows)
		return KittyTransmit(id, fitted, CellSize{}, cols, rows, tmux)()
	}
}

// Render renders the given image within the specified dimensions using the
// specified encoding.
func (e Encoding) Render(id string, cols, rows int) string {
	key := imageKey{id: id, cols: cols, rows: rows}
	cachedMutex.RLock()
	cached, ok := cachedImages[key]
	cachedMutex.RUnlock()
	if !ok {
		return ""
	}

	img := cached.img

	switch e {
	case EncodingBlocks:
		canvas := paintbrush.New()
		canvas.SetImage(img)
		canvas.SetWidth(cols)
		canvas.SetHeight(rows)
		canvas.Weights = map[rune]float64{
			'': .95,
			'': .95,
			'▁': .9,
			'▂': .9,
			'▃': .9,
			'▄': .9,
			'▅': .9,
			'▆': .85,
			'█': .85,
			'▊': .95,
			'▋': .95,
			'▌': .95,
			'▍': .95,
			'▎': .95,
			'▏': .95,
			'●': .95,
			'◀': .95,
			'▲': .95,
			'▶': .95,
			'▼': .9,
			'○': .8,
			'◉': .95,
			'◧': .9,
			'◨': .9,
			'◩': .9,
			'◪': .9,
		}
		canvas.Paint()
		return strings.TrimSpace(canvas.GetResult())
	case EncodingKitty:
		return KittyPlaceholders(id, cols, rows)
	default:
		return ""
	}
}

// KittyTransmit encodes img as a Kitty graphics transmission and returns a
// tea.Cmd that emits it as a raw escape sequence. Unlike Encoding.Transmit
// this never reads or writes the file picker's shared image cache: callers
// that need to avoid re-transmitting an unchanged image (e.g. chat message
// items, which must survive the file picker's ResetCache on close) must
// track that themselves instead.
func KittyTransmit(id string, img image.Image, cs CellSize, cols, rows int, tmux bool) tea.Cmd {
	if img == nil {
		return nil
	}

	return func() tea.Msg {
		fitted := img
		if cs.Width > 0 && cs.Height > 0 {
			fitted = imaging.Fit(img, cols*cs.Width, rows*cs.Height, imaging.Lanczos)
		}

		var buf bytes.Buffer
		bounds := fitted.Bounds()
		imgID := int(imageKey{id: id, cols: cols, rows: rows}.Hash())
		if err := kitty.EncodeGraphics(&buf, fitted, &kitty.Options{
			ID:               imgID,
			Action:           kitty.TransmitAndPut,
			Transmission:     kitty.Direct,
			Format:           kitty.RGBA,
			ImageWidth:       bounds.Dx(),
			ImageHeight:      bounds.Dy(),
			Columns:          cols,
			Rows:             rows,
			VirtualPlacement: true,
			Quite:            1,
			Chunk:            true,
			ChunkFormatter: func(chunk string) string {
				if tmux {
					return ansi.TmuxPassthrough(chunk)
				}
				return chunk
			},
		}); err != nil {
			slog.Error("Failed to encode image for kitty graphics", "err", err)
			return util.InfoMsg{
				Type: util.InfoTypeError,
				Msg:  "failed to encode image",
			}
		}

		return tea.RawMsg{Msg: buf.String()}
	}
}

// KittyPlaceholders returns the Kitty Unicode placeholder grid that paints a
// previously KittyTransmit-ed image into cols x rows cells. id, cols, and
// rows must match the KittyTransmit call that put the image on the
// terminal, since the placeholder color-encodes the same hash used as that
// transmission's image ID. Unlike Encoding.Render this never reads the file
// picker's shared image cache.
func KittyPlaceholders(id string, cols, rows int) string {
	hashedID := int(imageKey{id: id, cols: cols, rows: rows}.Hash())

	var fg color.Color
	extra, r, g, b := hashedID>>24&0xff, hashedID>>16&0xff, hashedID>>8&0xff, hashedID&0xff

	if hashedID <= 255 {
		fg = ansi.IndexedColor(b)
	} else {
		fg = color.RGBA{
			R: uint8(r), //nolint:gosec
			G: uint8(g), //nolint:gosec
			B: uint8(b), //nolint:gosec
			A: 0xff,
		}
	}

	fgStyle := ansi.NewStyle().ForegroundColor(fg).String()

	var buf bytes.Buffer
	for y := range rows {
		// As an optimization, we only write the fg color sequence id, and
		// column-row data once on the first cell. The terminal will handle
		// the rest.
		buf.WriteString(fgStyle)
		buf.WriteRune(kitty.Placeholder)
		buf.WriteRune(kitty.Diacritic(y))
		buf.WriteRune(kitty.Diacritic(0))
		if extra > 0 {
			buf.WriteRune(kitty.Diacritic(extra))
		}
		for x := 1; x < cols; x++ {
			buf.WriteString(fgStyle)
			buf.WriteRune(kitty.Placeholder)
		}
		if y < rows-1 {
			buf.WriteByte('\n')
		}
	}

	return buf.String()
}
