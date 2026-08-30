package dialog

import (
	"image"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

const cfgProviderID = "acme"

func newModelConfigDialog(t *testing.T, base catwalk.Model) *ModelConfig {
	t.Helper()
	s := styles.CharmtonePantera()
	return NewModelConfig(
		&common.Common{Styles: &s},
		true,
		catwalk.Provider{ID: cfgProviderID, Name: "Acme"},
		config.SelectedModel{Provider: cfgProviderID, Model: "typed-model"},
		base,
		config.ModelMain,
	)
}

func fieldValues(m *ModelConfig) []string {
	out := make([]string, len(m.inputs))
	for i := range m.inputs {
		out[i] = m.inputs[i].Value()
	}
	return out
}

func submit(m *ModelConfig) Action {
	return m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})
}

// TestAnUnknownModelGetsTheDefaults is the case that motivates the whole
// step: a hand-typed model has no catalog entry, so these numbers are
// the only ones it will ever have.
func TestAnUnknownModelGetsTheDefaults(t *testing.T) {
	t.Parallel()

	m := newModelConfigDialog(t, catwalk.Model{})

	require.Equal(t, []string{"", "32768", "1048576"}, fieldValues(m))
}

// TestAKnownModelPrefillsFromItsCatalogEntry keeps the step from
// overwriting metadata the catalog already got right.
func TestAKnownModelPrefillsFromItsCatalogEntry(t *testing.T) {
	t.Parallel()

	m := newModelConfigDialog(t, catwalk.Model{
		ID:                     "typed-model",
		Name:                   "Typed",
		ContextWindow:          200000,
		DefaultMaxTokens:       8192,
		DefaultReasoningEffort: "medium",
	})

	require.Equal(t, []string{"medium", "8192", "200000"}, fieldValues(m))
}

func TestSubmittingEmitsTheConfiguredModel(t *testing.T) {
	t.Parallel()

	m := newModelConfigDialog(t, catwalk.Model{})
	m.inputs[modelCfgFieldMaxTokens].SetValue("4096")
	m.inputs[modelCfgFieldContextWindow].SetValue("65536")

	action, ok := submit(m).(ActionConfigureModel)
	require.True(t, ok)

	require.Equal(t, int64(4096), action.Model.MaxTokens)
	require.Equal(t, "typed-model", action.Catwalk.ID)
	require.Equal(t, "typed-model", action.Catwalk.Name)
	require.Equal(t, int64(65536), action.Catwalk.ContextWindow)
	require.Equal(t, int64(4096), action.Catwalk.DefaultMaxTokens)
	require.Equal(t, config.ModelMain, action.ModelType)
}

// TestBlankNumbersFallBackToTheDefaults treats an emptied field as "I
// don't care", not as zero — zero would be a model that cannot answer.
func TestBlankNumbersFallBackToTheDefaults(t *testing.T) {
	t.Parallel()

	m := newModelConfigDialog(t, catwalk.Model{})
	m.inputs[modelCfgFieldMaxTokens].SetValue("")
	m.inputs[modelCfgFieldContextWindow].SetValue("  ")

	action, ok := submit(m).(ActionConfigureModel)
	require.True(t, ok)
	require.Equal(t, defaultModelMaxTokens, action.Model.MaxTokens)
	require.Equal(t, defaultModelContextWindow, action.Catwalk.ContextWindow)
}

func TestInvalidNumbersBlockTheSubmission(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		field int
		value string
	}{
		{"non-numeric max tokens", modelCfgFieldMaxTokens, "many"},
		{"zero max tokens", modelCfgFieldMaxTokens, "0"},
		{"negative context window", modelCfgFieldContextWindow, "-1"},
		{"fractional context window", modelCfgFieldContextWindow, "1.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := newModelConfigDialog(t, catwalk.Model{})
			m.inputs[tc.field].SetValue(tc.value)

			require.Nil(t, submit(m), "an unusable value must not be accepted")
			require.NotEmpty(t, m.err)
			require.Equal(t, tc.field, m.focused, "focus must land on the field to fix")
		})
	}
}

// TestAnEffortIsDeclaredSupported is the silent-failure guard: the agent
// drops a reasoning effort the model is not declared to support, so
// writing the effort without the declaration configures nothing.
func TestAnEffortIsDeclaredSupported(t *testing.T) {
	t.Parallel()

	m := newModelConfigDialog(t, catwalk.Model{})
	m.inputs[modelCfgFieldEffort].SetValue("xhigh")

	action, ok := submit(m).(ActionConfigureModel)
	require.True(t, ok)

	require.Equal(t, "xhigh", action.Model.ReasoningEffort)
	require.True(t, action.Catwalk.CanReason)
	require.Contains(t, action.Catwalk.ReasoningLevels, "xhigh")
	require.Equal(t, "xhigh", action.Catwalk.DefaultReasoningEffort)
}

// TestAnEffortIsNotDuplicatedInTheLevels keeps re-confirming a model the
// catalog already describes from growing its level list every time.
func TestAnEffortIsNotDuplicatedInTheLevels(t *testing.T) {
	t.Parallel()

	m := newModelConfigDialog(t, catwalk.Model{
		CanReason:       true,
		ReasoningLevels: []string{"low", "high"},
	})
	m.inputs[modelCfgFieldEffort].SetValue("high")

	action := submit(m).(ActionConfigureModel)
	require.Equal(t, []string{"low", "high"}, action.Catwalk.ReasoningLevels)
}

// TestABlankEffortLeavesReasoningAlone: blank means "none", and must not
// promote a non-reasoning model into one.
func TestABlankEffortLeavesReasoningAlone(t *testing.T) {
	t.Parallel()

	m := newModelConfigDialog(t, catwalk.Model{})
	m.inputs[modelCfgFieldEffort].SetValue("")

	action := submit(m).(ActionConfigureModel)
	require.Empty(t, action.Model.ReasoningEffort)
	require.False(t, action.Catwalk.CanReason)
	require.Empty(t, action.Catwalk.ReasoningLevels)
}

func TestFocusWrapsAcrossTheFields(t *testing.T) {
	t.Parallel()

	m := newModelConfigDialog(t, catwalk.Model{})
	next := tea.KeyPressMsg{Code: tea.KeyTab}

	require.Equal(t, modelCfgFieldEffort, m.focused)
	m.HandleMsg(next)
	require.Equal(t, modelCfgFieldMaxTokens, m.focused)
	m.HandleMsg(next)
	require.Equal(t, modelCfgFieldContextWindow, m.focused)
	m.HandleMsg(next)
	require.Equal(t, modelCfgFieldEffort, m.focused, "focus must wrap around")

	m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, modelCfgFieldContextWindow, m.focused)
}

func TestEscapeClosesTheDialog(t *testing.T) {
	t.Parallel()

	m := newModelConfigDialog(t, catwalk.Model{})
	require.IsType(t, ActionClose{}, m.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEscape}))
}

// drawModelConfig renders the onboarding form and returns its rows as
// plain text, along with the cursor the dialog placed.
func drawModelConfig(t *testing.T, m *ModelConfig) ([]string, *tea.Cursor) {
	t.Helper()

	const w, h = 80, 24
	scr := uv.NewScreenBuffer(w, h)
	cur := m.Draw(scr, image.Rect(0, 0, w, h))

	rows := strings.Split(scr.Render(), "\n")
	for i, row := range rows {
		rows[i] = strings.TrimRight(ansi.Strip(row), " ")
	}
	return rows, cur
}

// The terminal cursor has to land inside the focused field's input. The
// offset per field is derived from the input's style, and when it drifts
// from what the form actually emits the cursor parks on the next field's
// label instead.
func TestTheCursorLandsInTheFocusedInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		field int
		value string
	}{
		{"effort", modelCfgFieldEffort, "medium"},
		{"max tokens", modelCfgFieldMaxTokens, "8192"},
		{"context window", modelCfgFieldContextWindow, "200000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := newModelConfigDialog(t, catwalk.Model{
				ContextWindow:          200000,
				DefaultMaxTokens:       8192,
				DefaultReasoningEffort: "medium",
			})
			m.focusInput(tc.field)

			rows, cur := drawModelConfig(t, m)
			require.NotNil(t, cur)
			require.Less(t, cur.Y, len(rows))

			row := rows[cur.Y]
			require.NotEqual(t, modelCfgFieldLabels[tc.field], strings.TrimSpace(row),
				"the cursor sits on the field label instead of its input")
			require.Contains(t, row, tc.value,
				"the cursor row is not the focused field's input")
			// The field is prefilled, so the caret belongs after the
			// text the user is about to edit, not before it.
			require.Equal(t, strings.Index(row, tc.value)+len(tc.value), cur.X,
				"the cursor is not at the end of the prefilled value")
		})
	}
}

// Every block in the form is inset by one column. The labels carry no
// inset of their own — the input's comes from a margin, which does not
// reach them — so a label rendered flush left makes the form ragged.
func TestLabelsAlignWithTheirInputs(t *testing.T) {
	t.Parallel()

	m := newModelConfigDialog(t, catwalk.Model{})
	rows, _ := drawModelConfig(t, m)

	for _, label := range modelCfgFieldLabels {
		labelRow := -1
		for i, row := range rows {
			if strings.TrimSpace(row) == label {
				labelRow = i
				break
			}
		}
		require.NotEqual(t, -1, labelRow, "label %q was not rendered", label)

		// The input carries a vertical margin, so it is not the row
		// immediately below its label.
		inputRow := -1
		for i := labelRow + 1; i < len(rows); i++ {
			if strings.Contains(rows[i], "> ") {
				inputRow = i
				break
			}
		}
		require.NotEqual(t, -1, inputRow, "no input follows label %q", label)
		require.Equal(t, indentOf(rows[labelRow]), indentOf(rows[inputRow]),
			"label %q is not aligned with its input", label)
	}
}

// A validation error has to reach the screen: a submission that silently
// does nothing reads as a frozen dialog.
func TestTheValidationErrorIsRendered(t *testing.T) {
	t.Parallel()

	m := newModelConfigDialog(t, catwalk.Model{})
	m.inputs[modelCfgFieldMaxTokens].SetValue("many")
	require.Nil(t, submit(m))

	rows, _ := drawModelConfig(t, m)
	require.Contains(t, strings.Join(rows, "\n"), m.err)
}

func TestTheHelpAdvertisesTheFormKeys(t *testing.T) {
	t.Parallel()

	m := newModelConfigDialog(t, catwalk.Model{})
	keys := helpKeys(m.ShortHelp())
	require.Contains(t, keys, "enter")
	require.Contains(t, keys, "esc")
}
