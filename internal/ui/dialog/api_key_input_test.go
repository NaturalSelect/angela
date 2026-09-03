package dialog

import (
	"fmt"
	"image"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

const (
	apiKeyProviderID      = "acme"
	apiKeyCatalogEndpoint = "https://api.acme.com"
)

// apiKeyWorkspace records the config writes the dialog performs, in order,
// so tests can pin both the values and their sequence.
type apiKeyWorkspace struct {
	workspace.Workspace

	cfg   *config.Config
	calls []string
}

func (w *apiKeyWorkspace) Config() *config.Config { return w.cfg }

func (w *apiKeyWorkspace) SetConfigField(_ config.Scope, key string, value any) error {
	w.calls = append(w.calls, fmt.Sprintf("set %s=%v", key, value))
	return nil
}

func (w *apiKeyWorkspace) RemoveConfigField(_ config.Scope, key string) error {
	w.calls = append(w.calls, "remove "+key)
	return nil
}

func (w *apiKeyWorkspace) SetProviderAPIKey(_ config.Scope, providerID string, apiKey any) error {
	w.calls = append(w.calls, fmt.Sprintf("apikey %s=%v", providerID, apiKey))
	return nil
}

// apiKeyConfig mirrors what configureProviders leaves behind: built-in
// providers always carry a BaseURL, equal to the catalog endpoint unless
// the user overrode it.
func apiKeyConfig(baseURL string) *config.Config {
	providers := csync.NewMap[string, config.ProviderConfig]()
	if baseURL != "" {
		providers.Set(apiKeyProviderID, config.ProviderConfig{
			ID:      apiKeyProviderID,
			Name:    "Acme",
			BaseURL: baseURL,
		})
	}
	return &config.Config{Providers: providers}
}

func newAPIKeyDialog(t *testing.T, ws *apiKeyWorkspace) *APIKeyInput {
	t.Helper()
	return newAPIKeyDialogIn(t, ws, false)
}

func newAPIKeyDialogIn(t *testing.T, ws *apiKeyWorkspace, onboarding bool) *APIKeyInput {
	t.Helper()
	if ws.cfg == nil {
		ws.cfg = apiKeyConfig("")
	}
	s := styles.CharmtonePantera()
	provider := catwalk.Provider{
		ID:          apiKeyProviderID,
		Name:        "Acme",
		Type:        catwalk.TypeAnthropic,
		APIEndpoint: apiKeyCatalogEndpoint,
	}
	d, _ := NewAPIKeyInput(
		&common.Common{Workspace: ws, Styles: &s},
		onboarding,
		provider,
		config.SelectedModel{},
		config.SlotMain,
	)
	return d
}

// drawAPIKeyOnboarding renders the onboarding form and returns its rows as
// plain text, along with the cursor the dialog placed.
func drawAPIKeyOnboarding(t *testing.T, d *APIKeyInput) ([]string, *tea.Cursor) {
	t.Helper()

	const w, h = 80, 24
	scr := uv.NewScreenBuffer(w, h)
	cur := d.Draw(scr, image.Rect(0, 0, w, h))

	rows := strings.Split(scr.Render(), "\n")
	for i, row := range rows {
		rows[i] = strings.TrimRight(ansi.Strip(row), " ")
	}
	return rows, cur
}

func indentOf(row string) int {
	return len(row) - len(strings.TrimLeft(row, " "))
}

// The terminal cursor has to land inside the focused field's input. The
// offset per field is derived from the input's style, and when it drifts
// from what the form actually emits the cursor parks on the next field's
// label instead.
func TestAPIKeyInputOnboardingCursorLandsInTheFocusedInput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		field       int
		placeholder string
	}{
		{"api key", apiKeyFieldKey, "Enter your API key..."},
		{"base url", apiKeyFieldBaseURL, apiKeyCatalogEndpoint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := newAPIKeyDialogIn(t, &apiKeyWorkspace{}, true)
			d.focusInput(tc.field)

			rows, cur := drawAPIKeyOnboarding(t, d)
			require.NotNil(t, cur)
			require.Less(t, cur.Y, len(rows))

			row := rows[cur.Y]
			require.NotEqual(t, apiKeyFieldLabels[tc.field], strings.TrimSpace(row),
				"the cursor sits on the field label instead of its input")
			require.Contains(t, row, tc.placeholder,
				"the cursor row is not the focused field's input")
			require.Equal(t, strings.Index(row, tc.placeholder), cur.X,
				"the cursor is not at the first column of the input text")
		})
	}
}

// Every block in the form is inset by one column. The labels carry no inset
// of their own — the input's comes from a margin, which does not reach them
// — so a label rendered flush left makes the whole form look ragged.
func TestAPIKeyInputLabelsAlignWithTheirInputs(t *testing.T) {
	t.Parallel()

	d := newAPIKeyDialogIn(t, &apiKeyWorkspace{}, true)
	rows, _ := drawAPIKeyOnboarding(t, d)

	for _, label := range apiKeyFieldLabels {
		labelRow := -1
		for i, row := range rows {
			if strings.TrimSpace(row) == label {
				labelRow = i
				break
			}
		}
		require.NotEqual(t, -1, labelRow, "label %q was not rendered", label)

		inputRow := -1
		for i := labelRow + 1; i < len(rows); i++ {
			if strings.Contains(rows[i], "> ") {
				inputRow = i
				break
			}
		}
		require.NotEqual(t, -1, inputRow, "label %q has no input below it", label)

		require.Equal(t, indentOf(rows[inputRow]), indentOf(rows[labelRow]),
			"label %q is not aligned with its input", label)
	}
}

func TestAPIKeyInputBaseURLFallsBackToCatalog(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		typed string
		want  string
	}{
		{"left empty", "", apiKeyCatalogEndpoint},
		{"only whitespace", "   ", apiKeyCatalogEndpoint},
		{"custom relay", "https://relay.example.com", "https://relay.example.com"},
		{"padded custom relay", "  https://relay.example.com  ", "https://relay.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := newAPIKeyDialog(t, &apiKeyWorkspace{})
			d.inputs[apiKeyFieldBaseURL].SetValue(tc.typed)

			require.Equal(t, tc.want, d.baseURL())
		})
	}
}

func TestAPIKeyInputPrefillsOnlyCustomBaseURL(t *testing.T) {
	t.Parallel()

	t.Run("catalog default is not prefilled", func(t *testing.T) {
		t.Parallel()

		d := newAPIKeyDialog(t, &apiKeyWorkspace{cfg: apiKeyConfig(apiKeyCatalogEndpoint)})
		require.Empty(t, d.inputs[apiKeyFieldBaseURL].Value())
	})

	t.Run("override is prefilled", func(t *testing.T) {
		t.Parallel()

		d := newAPIKeyDialog(t, &apiKeyWorkspace{cfg: apiKeyConfig("https://relay.example.com")})
		require.Equal(t, "https://relay.example.com", d.inputs[apiKeyFieldBaseURL].Value())
	})

	t.Run("unconfigured provider is not prefilled", func(t *testing.T) {
		t.Parallel()

		d := newAPIKeyDialog(t, &apiKeyWorkspace{})
		require.Empty(t, d.inputs[apiKeyFieldBaseURL].Value())
	})
}

func TestAPIKeyInputFocusWrapsBetweenFields(t *testing.T) {
	t.Parallel()

	tab := tea.KeyPressMsg{Code: tea.KeyTab}
	shiftTab := tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}

	d := newAPIKeyDialog(t, &apiKeyWorkspace{})
	require.Equal(t, apiKeyFieldKey, d.focused)

	d.HandleMsg(tab)
	require.Equal(t, apiKeyFieldBaseURL, d.focused)

	d.HandleMsg(tab)
	require.Equal(t, apiKeyFieldKey, d.focused, "tab past the last field wraps to the first")

	d.HandleMsg(shiftTab)
	require.Equal(t, apiKeyFieldBaseURL, d.focused, "shift+tab before the first field wraps to the last")
}

func TestAPIKeyInputEnterSubmitsFromEitherField(t *testing.T) {
	t.Parallel()

	// The base URL is optional, so enter must submit wherever the cursor
	// sits rather than walking the user through every field.
	for _, field := range []int{apiKeyFieldKey, apiKeyFieldBaseURL} {
		d := newAPIKeyDialog(t, &apiKeyWorkspace{})
		d.focusInput(field)

		action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

		require.Equal(t, ActionChangeAPIKeyState{State: APIKeyInputStateVerifying}, action)
	}
}

func TestAPIKeyInputPersistsBaseURLBeforeAPIKey(t *testing.T) {
	t.Parallel()

	const field = "providers." + apiKeyProviderID + ".base_url"

	t.Run("custom base URL is written before the key", func(t *testing.T) {
		t.Parallel()

		ws := &apiKeyWorkspace{}
		d := newAPIKeyDialog(t, ws)
		d.inputs[apiKeyFieldKey].SetValue("sk-test")
		d.inputs[apiKeyFieldBaseURL].SetValue("https://relay.example.com")

		require.IsType(t, ActionSelectModel{}, d.saveKeyAndContinue())
		require.Equal(t, []string{
			"apikey " + apiKeyProviderID + "=sk-test",
			"set " + field + "=https://relay.example.com",
		}, ws.calls)
	})

	t.Run("empty base URL writes nothing", func(t *testing.T) {
		t.Parallel()

		ws := &apiKeyWorkspace{}
		d := newAPIKeyDialog(t, ws)
		d.inputs[apiKeyFieldKey].SetValue("sk-test")

		require.IsType(t, ActionSelectModel{}, d.saveKeyAndContinue())
		require.Equal(t, []string{"apikey " + apiKeyProviderID + "=sk-test"}, ws.calls,
			"an untouched base URL must not overwrite the catalog default with an empty string")
	})

	t.Run("cleared override is removed", func(t *testing.T) {
		t.Parallel()

		ws := &apiKeyWorkspace{cfg: apiKeyConfig("https://relay.example.com")}
		d := newAPIKeyDialog(t, ws)
		d.inputs[apiKeyFieldKey].SetValue("sk-test")
		d.inputs[apiKeyFieldBaseURL].SetValue("")

		require.IsType(t, ActionSelectModel{}, d.saveKeyAndContinue())
		require.Equal(t, []string{
			"apikey " + apiKeyProviderID + "=sk-test",
			"remove " + field,
		}, ws.calls)
	})
}

func TestAPIKeyInputSaveAnywayOnlyEscapesTheErrorState(t *testing.T) {
	t.Parallel()

	ctrlY := tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl}

	t.Run("ignored while editing", func(t *testing.T) {
		t.Parallel()

		ws := &apiKeyWorkspace{}
		d := newAPIKeyDialog(t, ws)

		d.HandleMsg(ctrlY)
		require.Empty(t, ws.calls)
	})

	t.Run("saves after a failed check", func(t *testing.T) {
		t.Parallel()

		ws := &apiKeyWorkspace{}
		d := newAPIKeyDialog(t, ws)
		d.inputs[apiKeyFieldKey].SetValue("sk-test")
		d.inputs[apiKeyFieldBaseURL].SetValue("https://relay.example.com")
		d.state = APIKeyInputStateError

		require.IsType(t, ActionSelectModel{}, d.HandleMsg(ctrlY))
		require.Equal(t, []string{
			"apikey " + apiKeyProviderID + "=sk-test",
			"set providers." + apiKeyProviderID + ".base_url=https://relay.example.com",
		}, ws.calls)
	})

	t.Run("advertised only in the error state", func(t *testing.T) {
		t.Parallel()

		d := newAPIKeyDialog(t, &apiKeyWorkspace{})
		require.NotContains(t, helpKeys(d.ShortHelp()), "ctrl+y")

		d.state = APIKeyInputStateError
		require.Contains(t, helpKeys(d.ShortHelp()), "ctrl+y")
	})
}

func helpKeys(bindings []key.Binding) []string {
	keys := make([]string, 0, len(bindings))
	for _, b := range bindings {
		keys = append(keys, b.Help().Key)
	}
	return keys
}
