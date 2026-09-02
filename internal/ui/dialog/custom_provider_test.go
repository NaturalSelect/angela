package dialog

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/NaturalSelect/angela/internal/workspace"
	"github.com/stretchr/testify/require"
)

const customProviderCatalogID = "known-co"

// customProviderWorkspace records every config write and, when survives
// is true, mirrors it into cfg so a later Config().Providers.Get(id)
// observes it the way a real reload would. Leaving survives false
// models an endpoint whose provider was written but discovered no
// models, which the config loader drops on the very next reload.
type customProviderWorkspace struct {
	workspace.Workspace

	cfg      *config.Config
	calls    []string
	survives bool
	setErr   error
}

func (w *customProviderWorkspace) Config() *config.Config { return w.cfg }

func (w *customProviderWorkspace) SetConfigField(_ config.Scope, key string, value any) error {
	w.calls = append(w.calls, key)
	if w.setErr != nil {
		return w.setErr
	}
	if w.survives {
		pc, _ := value.(config.ProviderConfig)
		pc.ID = strings.TrimPrefix(key, "providers.")
		w.cfg.Providers.Set(pc.ID, pc)
	}
	return nil
}

func newCustomProviderDialog(t *testing.T, ws *customProviderWorkspace, catalog []catwalk.Provider) *CustomProvider {
	t.Helper()
	if ws.cfg == nil {
		ws.cfg = &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}
	}
	s := styles.CharmtonePantera()
	d, _ := NewCustomProvider(&common.Common{Workspace: ws, Styles: &s}, true, catalog)
	return d
}

// TestCustomProviderValidationBlocksSubmit pins every local check the
// form runs before it will ever touch the network or the config: none
// of them may let a submit through.
func TestCustomProviderValidationBlocksSubmit(t *testing.T) {
	t.Parallel()

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}
	catalog := []catwalk.Provider{{ID: customProviderCatalogID, Name: "Known Co"}}

	configuredCfg := func() *config.Config {
		cfg := &config.Config{Providers: csync.NewMap[string, config.ProviderConfig]()}
		cfg.Providers.Set("taken", config.ProviderConfig{ID: "taken", Name: "Taken"})
		return cfg
	}

	for _, tc := range []struct {
		name    string
		setup   func(d *CustomProvider)
		wantErr string
	}{
		{
			name:    "empty name",
			setup:   func(d *CustomProvider) {},
			wantErr: "name is required",
		},
		{
			name: "name with invalid characters",
			setup: func(d *CustomProvider) {
				d.inputs[customProviderFieldName].SetValue("my.server")
				d.inputs[customProviderFieldBaseURL].SetValue("https://api.example.com")
			},
			wantErr: "must start with a letter or digit",
		},
		{
			name: "collides with a configured provider",
			setup: func(d *CustomProvider) {
				d.inputs[customProviderFieldName].SetValue("taken")
				d.inputs[customProviderFieldBaseURL].SetValue("https://api.example.com")
			},
			wantErr: "already exists",
		},
		{
			name: "collides with the catalog",
			setup: func(d *CustomProvider) {
				d.inputs[customProviderFieldName].SetValue(customProviderCatalogID)
				d.inputs[customProviderFieldBaseURL].SetValue("https://api.example.com")
			},
			wantErr: "already a known provider",
		},
		{
			name: "empty base URL",
			setup: func(d *CustomProvider) {
				d.inputs[customProviderFieldName].SetValue("my-server")
			},
			wantErr: "base URL is required",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ws := &customProviderWorkspace{cfg: configuredCfg()}
			d := newCustomProviderDialog(t, ws, catalog)
			tc.setup(d)

			action := d.HandleMsg(enter)
			require.Nil(t, action, "invalid input must not advance past the form")
			require.Contains(t, d.message, tc.wantErr)
			require.True(t, d.messageIsError)
			require.Empty(t, ws.calls, "validation failure must not touch the config")
		})
	}
}

// TestCustomProviderValidSubmitStartsVerification pins that a valid
// form probes the endpoint before writing anything, the same order the
// API key dialog uses.
func TestCustomProviderValidSubmitStartsVerification(t *testing.T) {
	t.Parallel()

	ws := &customProviderWorkspace{}
	d := newCustomProviderDialog(t, ws, nil)
	d.inputs[customProviderFieldName].SetValue("my-server")
	d.inputs[customProviderFieldBaseURL].SetValue("https://api.example.com")

	action := d.HandleMsg(tea.KeyPressMsg{Code: tea.KeyEnter})

	require.Equal(t, ActionChangeCustomProviderState{State: CustomProviderStateVerifying}, action)
	require.Empty(t, ws.calls, "verification happens before anything is written")
}

// TestCustomProviderSaveWritesTheWholeProviderAtOnce pins the atomic
// write: splitting the object into several writes would let an
// intermediate reload run model discovery without the API key or
// before the base URL landed.
func TestCustomProviderSaveWritesTheWholeProviderAtOnce(t *testing.T) {
	t.Parallel()

	ws := &customProviderWorkspace{survives: true}
	d := newCustomProviderDialog(t, ws, nil)
	d.inputs[customProviderFieldName].SetValue("My Server")
	d.inputs[customProviderFieldBaseURL].SetValue("https://api.example.com")
	d.inputs[customProviderFieldAPIKey].SetValue("sk-test")

	msg := d.save()

	require.Equal(t, []string{"providers.my-server"}, ws.calls,
		"the whole provider must land in a single write")

	pc, ok := ws.cfg.Providers.Get("my-server")
	require.True(t, ok)
	require.Equal(t, catwalk.TypeOpenAICompat, pc.Type)
	require.Equal(t, "https://api.example.com", pc.BaseURL)
	require.Equal(t, "sk-test", pc.APIKey)
	require.Empty(t, pc.Models, "models are left for auto-discovery when the endpoint succeeds")

	saved, ok := msg.(customProviderSavedMsg)
	require.True(t, ok)
	require.NoError(t, saved.err)
	require.False(t, saved.noModels)

	action := d.HandleMsg(msg)
	got, ok := action.(ActionSelectProvider)
	require.True(t, ok)
	require.True(t, got.Configured)
	require.Equal(t, catwalk.InferenceProvider("my-server"), got.Provider.ID)
}

// TestCustomProviderMissingModelsAsksForOneThenRetries covers the
// fallback for an endpoint that cannot be auto-discovered: the config
// loader drops any custom provider with zero models, so the form has
// to ask for one by hand instead of reporting a plain save failure.
func TestCustomProviderMissingModelsAsksForOneThenRetries(t *testing.T) {
	t.Parallel()

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}

	ws := &customProviderWorkspace{}
	d := newCustomProviderDialog(t, ws, nil)
	d.inputs[customProviderFieldName].SetValue("my-server")
	d.inputs[customProviderFieldBaseURL].SetValue("https://api.example.com")

	msg := d.save()
	require.Equal(t, []string{"providers.my-server"}, ws.calls)

	action := d.HandleMsg(msg)
	require.Nil(t, action)
	require.Equal(t, CustomProviderStateInitial, d.state)
	require.True(t, d.needsModel)
	require.Equal(t, customProviderFieldModelID, d.focused, "focus moves to the field the user must now fill in")
	require.False(t, d.messageIsError)
	require.Contains(t, d.message, "enter a model ID")

	// Submitting without a model id is rejected locally, without a
	// second write.
	action = d.HandleMsg(enter)
	require.Nil(t, action)
	require.Contains(t, d.message, "model ID is required")
	require.Equal(t, []string{"providers.my-server"}, ws.calls, "a local validation failure never reaches the config")

	// A filled-in model id skips straight to saving: the endpoint was
	// already probed once, so re-verifying an unchanged URL would only
	// delay the retry.
	d.inputs[customProviderFieldModelID].SetValue("llama-3.1-70b")
	action = d.HandleMsg(enter)
	require.Equal(t, ActionChangeCustomProviderState{State: CustomProviderStateSaving}, action)

	msg2 := d.save()
	require.Equal(t, []string{"providers.my-server", "providers.my-server"}, ws.calls)
	saved2, ok := msg2.(customProviderSavedMsg)
	require.True(t, ok)
	require.True(t, saved2.noModels, "the mock still never lands a reload, mirroring an endpoint that keeps yielding nothing")

	action = d.HandleMsg(msg2)
	cmdAction, ok := action.(ActionCmd)
	require.True(t, ok, "a repeated failure is reported instead of asking a third time")
	require.NotNil(t, cmdAction.Cmd)
}

// TestCustomProviderSaveErrorIsReported pins that a write failure
// returns the form to the verified stage so the user can retry, rather
// than leaving it stuck on the saving spinner.
func TestCustomProviderSaveErrorIsReported(t *testing.T) {
	t.Parallel()

	ws := &customProviderWorkspace{setErr: errors.New("disk full")}
	d := newCustomProviderDialog(t, ws, nil)
	d.inputs[customProviderFieldName].SetValue("my-server")
	d.inputs[customProviderFieldBaseURL].SetValue("https://api.example.com")

	msg := d.save()
	saved, ok := msg.(customProviderSavedMsg)
	require.True(t, ok)
	require.Error(t, saved.err)

	action := d.HandleMsg(msg)
	cmdAction, ok := action.(ActionCmd)
	require.True(t, ok)
	require.NotNil(t, cmdAction.Cmd)
	require.Equal(t, CustomProviderStateVerified, d.state)
}
