package image

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi/kitty"
	"github.com/stretchr/testify/require"
)

// Package-level note: cachedImages is process-global state shared by every
// test in this package. Tests that only ever miss the cache (fresh,
// unique keys) or never touch it are safe under t.Parallel(). Tests that
// seed an entry and then assert on it staying there are NOT marked
// parallel: TestResetCache/TestResetIdempotent (in image_test.go) clear
// the whole map while parallel, which would otherwise race with any
// assertion that depends on a specific key surviving between two calls.

func TestImageKeyID(t *testing.T) {
	t.Parallel()

	k := imageKey{id: "abc", cols: 3, rows: 4}
	require.Equal(t, "abc-3x4", k.ID())
}

func TestImageKeyHash(t *testing.T) {
	t.Parallel()

	k1 := imageKey{id: "abc", cols: 3, rows: 4}
	k2 := imageKey{id: "abc", cols: 3, rows: 4}
	require.Equal(t, k1.Hash(), k2.Hash(), "identical keys must hash identically")

	k3 := imageKey{id: "abc", cols: 3, rows: 5}
	require.NotEqual(t, k1.Hash(), k3.Hash(), "different keys should (in practice) hash differently")
}

func TestFitImage_NilImageReturnsNil(t *testing.T) {
	t.Parallel()

	require.Nil(t, fitImage("nil-img", nil, CellSize{Width: 10, Height: 10}, 2, 2))
}

func TestFitImage_ZeroCellSizeReturnsOriginalUnresized(t *testing.T) {
	t.Parallel()

	img := image.NewRGBA(image.Rect(0, 0, 50, 50))
	got := fitImage("zero-cell-"+t.Name(), img, CellSize{}, 5, 5)
	require.Same(t, image.Image(img), got)
}

// TestFitImage_ResizesToFitAndCaches is not parallel: it relies on its
// cache entry surviving between two fitImage calls.
func TestFitImage_ResizesToFitAndCaches(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 100, 50))
	id := "fit-resize-test"
	cs := CellSize{Width: 8, Height: 16}
	cols, rows := 5, 2 // max 40x32 px
	key := imageKey{id: id, cols: cols, rows: rows}
	t.Cleanup(func() {
		cachedMutex.Lock()
		delete(cachedImages, key)
		cachedMutex.Unlock()
	})

	got1 := fitImage(id, img, cs, cols, rows)
	require.NotNil(t, got1)
	b := got1.Bounds()
	require.LessOrEqual(t, b.Dx(), cols*cs.Width)
	require.LessOrEqual(t, b.Dy(), rows*cs.Height)

	// A second call with the same key must hit the cache and return the
	// exact same instance rather than resizing again.
	got2 := fitImage(id, img, cs, cols, rows)
	require.Same(t, got1, got2)

	require.True(t, HasTransmitted(id, cols, rows))
}

func TestHasTransmitted_FalseWhenAbsent(t *testing.T) {
	t.Parallel()

	require.False(t, HasTransmitted("never-transmitted-"+t.Name(), 9, 9))
}

// TestHasTransmitted_TrueWhenPresent is not parallel: it depends on its
// own seeded cache entry.
func TestHasTransmitted_TrueWhenPresent(t *testing.T) {
	id := "has-transmitted-test"
	cols, rows := 3, 3
	key := imageKey{id: id, cols: cols, rows: rows}
	cachedMutex.Lock()
	cachedImages[key] = cachedImage{img: image.NewRGBA(image.Rect(0, 0, 1, 1)), cols: cols, rows: rows}
	cachedMutex.Unlock()
	t.Cleanup(func() {
		cachedMutex.Lock()
		delete(cachedImages, key)
		cachedMutex.Unlock()
	})

	require.True(t, HasTransmitted(id, cols, rows))
}

func TestTransmit_NilImageReturnsNilCmd(t *testing.T) {
	t.Parallel()

	cmd := EncodingBlocks.Transmit("nil-transmit-"+t.Name(), nil, CellSize{Width: 1, Height: 1}, 1, 1, false)
	require.Nil(t, cmd)
}

// TestTransmit_AlreadyCachedReturnsNilCmd is not parallel: it seeds the
// cache and then depends on that entry to short-circuit Transmit.
func TestTransmit_AlreadyCachedReturnsNilCmd(t *testing.T) {
	id := "already-cached"
	cols, rows := 2, 2
	key := imageKey{id: id, cols: cols, rows: rows}
	cachedMutex.Lock()
	cachedImages[key] = cachedImage{img: image.NewRGBA(image.Rect(0, 0, 1, 1)), cols: cols, rows: rows}
	cachedMutex.Unlock()
	t.Cleanup(func() {
		cachedMutex.Lock()
		delete(cachedImages, key)
		cachedMutex.Unlock()
	})

	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	cmd := EncodingBlocks.Transmit(id, img, CellSize{Width: 1, Height: 1}, cols, rows, false)
	require.Nil(t, cmd)
}

// TestTransmit_BlocksEncodingCachesAndReturnsMsg is not parallel: it
// asserts on cache state written by the command it executes.
func TestTransmit_BlocksEncodingCachesAndReturnsMsg(t *testing.T) {
	id := "transmit-blocks"
	cols, rows := 2, 2
	key := imageKey{id: id, cols: cols, rows: rows}
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	t.Cleanup(func() {
		cachedMutex.Lock()
		delete(cachedImages, key)
		cachedMutex.Unlock()
	})

	cmd := EncodingBlocks.Transmit(id, img, CellSize{Width: 2, Height: 2}, cols, rows, false)
	require.NotNil(t, cmd)

	msg := cmd()
	transmitted, ok := msg.(TransmittedMsg)
	require.True(t, ok, "expected TransmittedMsg, got %T", msg)
	require.Equal(t, key.ID(), transmitted.ID)
	require.True(t, HasTransmitted(id, cols, rows))
}

// TestTransmit_KittyEncodingReturnsRawMsg is not parallel: the Kitty path
// writes a fit-cache entry as a side effect of encoding.
func TestTransmit_KittyEncodingReturnsRawMsg(t *testing.T) {
	id := "transmit-kitty"
	cols, rows := 2, 1
	key := imageKey{id: id, cols: cols, rows: rows}
	img := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	t.Cleanup(func() {
		cachedMutex.Lock()
		delete(cachedImages, key)
		cachedMutex.Unlock()
	})

	cmd := EncodingKitty.Transmit(id, img, CellSize{Width: 8, Height: 16}, cols, rows, false)
	require.NotNil(t, cmd)

	msg := cmd()
	raw, ok := msg.(tea.RawMsg)
	require.True(t, ok, "expected tea.RawMsg, got %T", msg)
	s, ok := raw.Msg.(string)
	require.True(t, ok)
	require.NotEmpty(t, s)
	require.True(t, strings.HasPrefix(s, "\x1b_G"), "kitty graphics payload must open with the APC escape")
}

// TestTransmit_KittyEncodingTmuxWrapsPassthrough is not parallel for the
// same reason as the Kitty test above.
func TestTransmit_KittyEncodingTmuxWrapsPassthrough(t *testing.T) {
	id := "transmit-kitty-tmux"
	cols, rows := 2, 1
	key := imageKey{id: id, cols: cols, rows: rows}
	img := image.NewNRGBA(image.Rect(0, 0, 20, 20))
	t.Cleanup(func() {
		cachedMutex.Lock()
		delete(cachedImages, key)
		cachedMutex.Unlock()
	})

	cmd := EncodingKitty.Transmit(id, img, CellSize{Width: 8, Height: 16}, cols, rows, true)
	require.NotNil(t, cmd)

	msg := cmd()
	raw, ok := msg.(tea.RawMsg)
	require.True(t, ok)
	s, ok := raw.Msg.(string)
	require.True(t, ok)
	require.NotEmpty(t, s)
	require.True(t, strings.HasPrefix(s, "\x1bPtmux;"), "tmux passthrough must wrap the escape sequence")
}

func TestRender_NotCachedReturnsEmpty(t *testing.T) {
	t.Parallel()

	require.Empty(t, EncodingBlocks.Render("render-missing-"+t.Name(), 3, 3))
}

// TestRender_UnknownEncodingReturnsEmpty is not parallel: it seeds the
// cache entry Render needs in order to reach the default case.
func TestRender_UnknownEncodingReturnsEmpty(t *testing.T) {
	id := "render-unknown"
	cols, rows := 1, 1
	key := imageKey{id: id, cols: cols, rows: rows}
	cachedMutex.Lock()
	cachedImages[key] = cachedImage{img: image.NewRGBA(image.Rect(0, 0, 1, 1)), cols: cols, rows: rows}
	cachedMutex.Unlock()
	t.Cleanup(func() {
		cachedMutex.Lock()
		delete(cachedImages, key)
		cachedMutex.Unlock()
	})

	var unknown Encoding = 99
	require.Empty(t, unknown.Render(id, cols, rows))
}

// TestRender_BlocksEncodingProducesOutput is not parallel: it seeds the
// cache entry Render reads.
func TestRender_BlocksEncodingProducesOutput(t *testing.T) {
	id := "render-blocks"
	cols, rows := 4, 2
	key := imageKey{id: id, cols: cols, rows: rows}
	img := image.NewNRGBA(image.Rect(0, 0, 40, 20))
	cachedMutex.Lock()
	cachedImages[key] = cachedImage{img: img, cols: cols, rows: rows}
	cachedMutex.Unlock()
	t.Cleanup(func() {
		cachedMutex.Lock()
		delete(cachedImages, key)
		cachedMutex.Unlock()
	})

	out := EncodingBlocks.Render(id, cols, rows)
	require.NotEmpty(t, out)
}

// TestRender_KittyEncodingProducesPlaceholders is not parallel: it seeds
// the cache entry Render reads.
func TestRender_KittyEncodingProducesPlaceholders(t *testing.T) {
	id := "render-kitty"
	cols, rows := 3, 2
	key := imageKey{id: id, cols: cols, rows: rows}
	cachedMutex.Lock()
	cachedImages[key] = cachedImage{img: image.NewRGBA(image.Rect(0, 0, 1, 1)), cols: cols, rows: rows}
	cachedMutex.Unlock()
	t.Cleanup(func() {
		cachedMutex.Lock()
		delete(cachedImages, key)
		cachedMutex.Unlock()
	})

	out := EncodingKitty.Render(id, cols, rows)
	require.NotEmpty(t, out)
	require.Equal(t, cols*rows, strings.Count(out, string(kitty.Placeholder)),
		"one placeholder glyph per cell")
	require.Equal(t, rows-1, strings.Count(out, "\n"))
}
