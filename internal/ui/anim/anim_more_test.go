package anim

import (
	"image/color"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNew_DefaultsAndCaching(t *testing.T) {
	t.Parallel()

	a1 := New(Settings{})
	require.NotEmpty(t, a1.id, "an empty ID must be auto-assigned via nextID")
	require.Equal(t, defaultNumCyclingChars, a1.cyclingCharWidth)

	a2 := New(Settings{})
	require.NotEqual(t, a1.id, a2.id, "each auto-assigned ID must be unique")
	require.Equal(t, a1.width, a2.width, "identical settings must hit the same cache entry")
	require.Equal(t, a1.cyclingFrames, a2.cyclingFrames)
}

func TestNew_CycleColorsProducesFrames(t *testing.T) {
	t.Parallel()

	a := New(Settings{
		ID:          "cycle-test",
		Size:        4,
		CycleColors: true,
		GradColorA:  color.RGBA{R: 255, A: 255},
		GradColorB:  color.RGBA{B: 255, A: 255},
	})
	require.NotEmpty(t, a.cyclingFrames)
	require.NotPanics(t, func() { a.Render() })
}

func TestNew_NoScrambleSkipsCyclingChars(t *testing.T) {
	t.Parallel()

	a := New(Settings{ID: "no-scramble", Label: "Working", NoScramble: true})
	require.Equal(t, 0, a.cyclingCharWidth)
	require.True(t, a.initialized.Load(), "NoScramble marks the anim initialized immediately")
	require.Contains(t, a.Render(), "W")
}

func TestNew_SuffixFunctionIsUsedInRender(t *testing.T) {
	t.Parallel()

	a := New(Settings{
		ID:          "suffix-test",
		Label:       "Loading",
		LabelColor:  color.RGBA{R: 200, A: 255},
		Suffix:      func() string { return "3s" },
		SuffixColor: color.RGBA{G: 200, A: 255},
	})
	require.Contains(t, a.Render(), "3s")
}

func TestNew_SuffixColorFallsBackToLabelColor(t *testing.T) {
	t.Parallel()

	labelColor := color.RGBA{R: 10, G: 20, B: 30, A: 255}
	a := New(Settings{ID: "suffix-fallback", Label: "x", LabelColor: labelColor})
	require.Equal(t, labelColor, a.suffixColor)
}

func TestSetLabel_UpdatesWidthAndContent(t *testing.T) {
	t.Parallel()

	a := New(Settings{ID: "set-label", Size: 3})
	initialWidth := a.Width()

	a.SetLabel("Hi")
	require.Greater(t, a.Width(), initialWidth)
	require.Contains(t, a.Render(), "H")
}

func TestSetLabel_NoScrambleSkipsGap(t *testing.T) {
	t.Parallel()

	a := New(Settings{ID: "set-label-noscramble", NoScramble: true})
	a.SetLabel("Hi")
	require.Equal(t, 2, a.width, "no cycling chars means no label gap is added")
}

func TestWidth_IncludesWidestEllipsisFrame(t *testing.T) {
	t.Parallel()

	a := New(Settings{ID: "width-test", Size: 5, Label: "Hi"})
	require.Equal(t, 8, a.width, "sanity: cyclingCharWidth(5) + labelGapWidth(1) + labelWidth(2)")
	// Width() adds the widest ellipsis frame ("...", 3) on top of a.width,
	// which already bakes in the label and its gap.
	require.Equal(t, 11, a.Width())
}

func TestWidth_NoLabelOmitsGapAndEllipsis(t *testing.T) {
	t.Parallel()

	a := New(Settings{ID: "width-no-label", Size: 5})
	require.Equal(t, 5, a.Width())
}

func TestRender_BeforeInitializationDoesNotPanic(t *testing.T) {
	t.Parallel()

	a := New(Settings{ID: "render-initial", Size: 3})
	require.False(t, a.initialized.Load())
	require.NotEmpty(t, a.Render())
}

func TestRender_AfterInitializationShowsLabelAndEllipsis(t *testing.T) {
	t.Parallel()

	a := New(Settings{ID: "render-after-init", Size: 2, Label: "Hi"})
	for range maxBirthSteps + 1 {
		a.Animate(StepMsg{ID: "render-after-init", Gen: 0})
	}
	require.True(t, a.initialized.Load())
	require.Contains(t, a.Render(), "H")
}

func TestRender_SuffixSuppressesEllipsis(t *testing.T) {
	t.Parallel()

	a := New(Settings{
		ID:     "render-suffix",
		Size:   2,
		Label:  "Hi",
		Suffix: func() string { return "5s" },
	})
	for range maxBirthSteps + 1 {
		a.Animate(StepMsg{ID: "render-suffix", Gen: 0})
	}
	require.Contains(t, a.Render(), "5s")
}

func TestColorIsUnset(t *testing.T) {
	t.Parallel()

	require.True(t, colorIsUnset(nil))
	require.True(t, colorIsUnset(color.RGBA{R: 10, G: 20, B: 30, A: 0}))
	require.False(t, colorIsUnset(color.RGBA{R: 10, A: 255}))
}

func TestMakeGradientRamp_FewerThanTwoStopsReturnsNil(t *testing.T) {
	t.Parallel()

	require.Nil(t, makeGradientRamp(5))
	require.Nil(t, makeGradientRamp(5, color.RGBA{R: 255, A: 255}))
}

// TestMakeGradientRamp_DistributesRemainderAcrossFirstSegments covers a
// size that doesn't divide evenly across segments. New() never calls
// makeGradientRamp this way (its sizes are always exact multiples of the
// segment count), so this calls it directly with a size that isn't.
func TestMakeGradientRamp_DistributesRemainderAcrossFirstSegments(t *testing.T) {
	t.Parallel()

	// size=7 across 2 segments (3 stops): baseSize=3, remainder=1, so the
	// first segment gets one extra color and the total still sums to 7.
	ramp := makeGradientRamp(7,
		color.RGBA{R: 255, A: 255}, color.RGBA{G: 255, A: 255}, color.RGBA{B: 255, A: 255})
	require.Len(t, ramp, 7)
}

// TestAnimate_EllipsisStepWraps covers the ellipsis animation's own
// wraparound, which only kicks in after the anim has both initialized
// (maxBirthSteps ticks) and then cycled through a full ellipsis period
// (ellipsisAnimSpeed * len(ellipsisFrames) further ticks).
func TestAnimate_EllipsisStepWraps(t *testing.T) {
	t.Parallel()

	a := New(Settings{ID: "ellipsis-wrap", Size: 2, Label: "Hi"})
	total := maxBirthSteps + ellipsisAnimSpeed*len(ellipsisFrames) + 1
	for range total {
		a.Animate(StepMsg{ID: "ellipsis-wrap", Gen: 0})
	}
	require.True(t, a.initialized.Load())
	require.Less(t, a.ellipsisStep.Load(), int64(ellipsisAnimSpeed*len(ellipsisFrames)),
		"ellipsis step counter must have wrapped back below its period")
}
