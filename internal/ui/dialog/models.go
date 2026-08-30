package dialog

import (
	"cmp"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/util"
	"github.com/NaturalSelect/angela/internal/workspace"
	uv "github.com/charmbracelet/ultraviolet"
)

// ModelsCatalogMsg carries the provider catalog to the models dialog
// once it has been fetched off the Update goroutine.
type ModelsCatalogMsg struct {
	Providers []catwalk.Provider
}

const (
	onboardingModelInputPlaceholder = "Find your fave, or type a model ID"
	modelInputPlaceholder           = "Choose a model"

	// customModelGroupTitle labels the entry built from whatever the
	// user typed when no catalog model carries that ID.
	customModelGroupTitle = "Custom model"
)

// ModelsID is the identifier for the model selection dialog.
const ModelsID = "models"

const defaultModelsDialogMaxWidth = 73

// Models represents a model selection dialog.
type Models struct {
	com          *common.Common
	isOnboarding bool

	// modelName is the model config the dialog writes to. Model config
	// names are an open set; this dialog edits main and leaves the rest
	// to agent-level config.
	modelName config.ModelConfigName
	providers []catwalk.Provider

	// restrictTo narrows the list to a single provider. Onboarding sets
	// it once the provider is picked; the global model switcher leaves
	// it empty and keeps listing everything.
	restrictTo catwalk.InferenceProvider

	// active is the agent the current session runs, or nil when it is
	// not known yet. It decides which model the list opens on, so the
	// highlighted entry is the one the session actually runs rather
	// than the global default.
	active *workspace.ActiveAgent

	// catalogLoaded reports whether providers arrived. Recents can only
	// be judged stale against a loaded catalog: pruning them against an
	// empty one would drop every entry the user has.
	catalogLoaded bool

	keyMap struct {
		UpDown   key.Binding
		Select   key.Binding
		Edit     key.Binding
		Next     key.Binding
		Previous key.Binding
		Close    key.Binding
	}
	list  *ModelsList
	input textinput.Model
	help  help.Model

	frame   *Frame
	metrics FrameMetrics

	// staleRecents are the recent-model entries that no longer resolve
	// to an available model. Building the item list must not write
	// config — that is a synchronous HTTP round-trip in client/server
	// mode — so the removal is deferred to a command.
	staleRecents []config.SelectedModel

	// restrictedProvider is the provider the custom entry is attributed
	// to. Only a restricted list has an unambiguous one.
	restrictedProvider catwalk.Provider
}

var _ Dialog = (*Models)(nil)

// NewModels creates a new Models dialog. active is the agent the
// current session runs, or nil when that is not known yet. The provider
// catalog is not loaded here — see InitialCmd.
func NewModels(com *common.Common, isOnboarding bool, active *workspace.ActiveAgent) *Models {
	t := com.Styles
	m := &Models{}
	m.com = com
	m.isOnboarding = isOnboarding
	m.modelName = config.ModelMain
	m.active = active

	m.frame = NewFrame(t, FrameSpec{
		Title:     "Switch Model",
		MaxWidth:  defaultModelsDialogMaxWidth,
		MaxHeight: defaultDialogHeight,
	})

	help := help.New()
	help.Styles = t.DialogHelpStyles()

	m.help = help
	m.list = NewModelsList(t)
	m.list.Focus()
	m.list.SetSelected(0)

	m.input = textinput.New()
	m.input.SetVirtualCursor(false)
	m.input.Placeholder = onboardingModelInputPlaceholder
	m.input.SetStyles(com.Styles.TextInput)
	m.input.Focus()

	m.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "confirm"),
	)
	m.keyMap.Edit = key.NewBinding(
		key.WithKeys("ctrl+e"),
		key.WithHelp("ctrl+e", "edit"),
	)
	m.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "choose"),
	)
	m.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	m.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	m.keyMap.Close = CloseKey

	m.setProviderItems()

	return m
}

// ID implements Dialog.
func (m *Models) ID() string {
	return ModelsID
}

// InitialCmd fetches the provider catalog off the Update goroutine.
// Listing providers can block for as long as a catalog refresh takes,
// which must never happen on the render loop; until it lands the dialog
// lists the providers already configured, so it always opens.
func (m *Models) InitialCmd() tea.Cmd {
	return m.loadCatalogCmd()
}

func (m *Models) loadCatalogCmd() tea.Cmd {
	cfg := m.com.Config()
	return func() tea.Msg {
		providers, err := config.Providers(cfg)
		if err != nil {
			// A stale catalog must not keep this dialog from working:
			// it is the only way for the user to choose a model.
			if len(providers) == 0 {
				return util.ReportError(fmt.Errorf("failed to get providers: %w", err))()
			}
			slog.Warn("Listing the previously known providers", "error", err)
		}
		return ModelsCatalogMsg{Providers: providers}
	}
}

// SetProviders installs the fetched catalog and rebuilds the list. It
// returns the command that records any recent-model entries that no
// longer resolve, or nil when every entry still does.
func (m *Models) SetProviders(providers []catwalk.Provider) tea.Cmd {
	m.providers = providers
	m.catalogLoaded = true
	m.setProviderItems()
	return m.pruneRecentsCmd()
}

// pruneRecentsCmd drops recent-model entries that no longer resolve to
// an available model. The write is an HTTP round-trip in client/server
// mode, so it never runs on the Update goroutine. It sends the dead
// entries rather than the surviving list: a model picked while the
// catalog was loading must not be erased by this write.
func (m *Models) pruneRecentsCmd() tea.Cmd {
	if len(m.staleRecents) == 0 {
		return nil
	}
	stale := m.staleRecents
	return func() tea.Msg {
		if err := m.com.Workspace.PruneRecentModels(config.ScopeGlobal, m.modelName, stale); err != nil {
			return util.ReportError(fmt.Errorf("failed to update recent models: %w", err))()
		}
		return nil
	}
}

// HandleMsg implements Dialog.
func (m *Models) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, m.keyMap.Previous):
			m.list.Focus()
			if m.list.IsSelectedFirst() {
				m.list.SelectLast()
			} else {
				m.list.SelectPrev()
			}
			m.list.ScrollToSelected()
		case key.Matches(msg, m.keyMap.Next):
			m.list.Focus()
			if m.list.IsSelectedLast() {
				m.list.SelectFirst()
			} else {
				m.list.SelectNext()
			}
			m.list.ScrollToSelected()
		case key.Matches(msg, m.keyMap.Select, m.keyMap.Edit):
			selectedItem := m.list.SelectedItem()
			if selectedItem == nil {
				break
			}

			modelItem, ok := selectedItem.(*ModelItem)
			if !ok {
				break
			}

			isEdit := key.Matches(msg, m.keyMap.Edit)

			return ActionSelectModel{
				Provider:       modelItem.prov,
				Model:          modelItem.SelectedModel(),
				ModelType:      modelItem.ModelConfigName(),
				ReAuthenticate: isEdit,
			}
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			value := m.input.Value()
			m.list.Focus()
			m.list.SetFilter(value)
			m.syncCustomItem(value)
			m.list.SelectFirst()
			m.list.ScrollToTop()
			return ActionCmd{cmd}
		}
	}
	return nil
}

// Cursor returns the cursor for the dialog.
func (m *Models) Cursor() *tea.Cursor {
	return InputCursor(m.com.Styles, m.input.Cursor())
}

// Draw implements [Dialog].
func (m *Models) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles
	m.metrics = m.frame.Measure(area)
	m.input.SetWidth(m.frame.InputTextWidth(m.input, m.metrics.ContentWidth))

	listHeight, listTotalHeight, _ := m.frame.SizeList(m.list, m.metrics)

	rc := m.frame.Context(m.metrics)

	if m.isOnboarding {
		titleText := t.Dialog.PrimaryText.Render("Now pick a model.")
		rc.AddPart(titleText)
	}

	inputView := t.Dialog.InputPrompt.Render(m.input.View())
	rc.AddPart(inputView)

	if !m.catalogLoaded && m.list.Len() == 0 {
		rc.AddPart(t.Dialog.SecondaryText.Render("Loading models…"))
	} else {
		listView := t.Dialog.List.Height(m.list.Height()).Render(m.list.Render())
		listView = m.frame.JoinScrollbar(listView, listHeight, listTotalHeight, listHeight, m.list.Offset())
		rc.AddPart(listView)
	}

	rc.Help = m.frame.RenderHelp(&m.help, m, m.metrics.ContentWidth)

	cur := m.Cursor()

	if m.isOnboarding {
		rc.Title = ""
		rc.TitleInfo = ""
		rc.IsOnboarding = true
		view := rc.Render()
		cur = adjustOnboardingInputCursor(t, cur)
		DrawOnboardingCursor(scr, area, view, cur)
	} else {
		view := rc.Render()
		DrawCenterCursor(scr, area, view, cur)
	}
	return cur
}

// ShortHelp returns the short help view.
func (m *Models) ShortHelp() []key.Binding {
	if m.isOnboarding {
		return []key.Binding{
			m.keyMap.UpDown,
			m.keyMap.Select,
		}
	}
	h := []key.Binding{
		m.keyMap.UpDown,
		m.keyMap.Select,
	}
	if m.isSelectedConfigured() {
		h = append(h, m.keyMap.Edit)
	}
	h = append(h, m.keyMap.Close)
	return h
}

// FullHelp returns the full help view.
func (m *Models) FullHelp() [][]key.Binding {
	return [][]key.Binding{m.ShortHelp()}
}

func (m *Models) isSelectedConfigured() bool {
	selectedItem := m.list.SelectedItem()
	if selectedItem == nil {
		return false
	}
	modelItem, ok := selectedItem.(*ModelItem)
	if !ok {
		return false
	}
	providerID := string(modelItem.prov.ID)
	_, isConfigured := m.com.Config().Providers.Get(providerID)
	return isConfigured
}

// allows reports whether a provider survives the current restriction.
func (m *Models) allows(id catwalk.InferenceProvider) bool {
	return m.restrictTo == "" || id == m.restrictTo
}

// RestrictToProvider narrows the list to a single provider's models. It
// also drops the recents group, which spans providers and would
// otherwise leak entries from the ones being hidden.
func (m *Models) RestrictToProvider(id catwalk.InferenceProvider) {
	m.restrictTo = id
	m.setProviderItems()
}

// setProviderItems sets the provider items in the list.
func (m *Models) setProviderItems() {
	t := m.com.Styles
	cfg := m.com.Config()

	var selectedItemID string
	// The list opens on what the session runs, not on the global
	// default: highlighting the global model makes confirming it look
	// like a no-op while it silently moves the session off its own.
	currentModel := cfg.Models[m.modelName]
	if m.active != nil && m.active.ModelName == m.modelName {
		currentModel = m.active.ModelCfg
	}
	recentItems := cfg.RecentModels[m.modelName]

	// Track providers already added to avoid duplicates
	addedProviders := make(map[string]bool)

	containsProviderFunc := func(id string) func(p catwalk.Provider) bool {
		return func(p catwalk.Provider) bool {
			return p.ID == catwalk.InferenceProvider(id)
		}
	}

	// itemsMap contains the keys of added model items.
	itemsMap := make(map[string]*ModelItem)
	groups := []ModelGroup{}
	for id, p := range cfg.Providers.Seq2() {
		if p.Disable {
			continue
		}
		if !m.allows(catwalk.InferenceProvider(id)) {
			continue
		}

		// Check if this provider is not in the known providers list
		if !slices.ContainsFunc(m.providers, containsProviderFunc(id)) {
			provider := p.ToProvider()

			// Add this unknown provider to the list
			name := cmp.Or(p.Name, id)

			addedProviders[id] = true

			group := NewModelGroup(t, name, true)
			for _, model := range p.Models {
				item := NewModelItem(t, provider, model, m.modelName, false)
				group.AppendItems(item)
				itemsMap[item.ID()] = item
				if model.ID == currentModel.Model && string(provider.ID) == currentModel.Provider {
					selectedItemID = item.ID()
				}
			}
			if len(group.Items) > 0 {
				groups = append(groups, group)
			}
		}
	}

	// Now add known providers from the predefined list.
	// Providers already has Hyper at the front of the list.
	for _, provider := range m.providers {
		providerID := string(provider.ID)
		if addedProviders[providerID] {
			continue
		}
		if !m.allows(provider.ID) {
			continue
		}

		providerConfig, providerConfigured := cfg.Providers.Get(providerID)
		if providerConfigured && providerConfig.Disable {
			continue
		}

		displayProvider := provider
		if providerConfigured {
			displayProvider.Name = cmp.Or(providerConfig.Name, displayProvider.Name)
			modelIndex := make(map[string]int, len(displayProvider.Models))
			for i, model := range displayProvider.Models {
				modelIndex[model.ID] = i
			}
			for _, model := range providerConfig.Models {
				if model.ID == "" {
					continue
				}
				if idx, ok := modelIndex[model.ID]; ok {
					if model.Name != "" {
						displayProvider.Models[idx].Name = model.Name
					}
					continue
				}
				model.Name = cmp.Or(model.Name, model.ID)
				displayProvider.Models = append(displayProvider.Models, model)
				modelIndex[model.ID] = len(displayProvider.Models) - 1
			}
		}

		name := cmp.Or(displayProvider.Name, providerID)

		group := NewModelGroup(t, name, providerConfigured)
		for _, model := range displayProvider.Models {
			item := NewModelItem(t, provider, model, m.modelName, false)
			group.AppendItems(item)
			itemsMap[item.ID()] = item
			if model.ID == currentModel.Model && string(provider.ID) == currentModel.Provider {
				selectedItemID = item.ID()
			}
		}

		groups = append(groups, group)
	}

	m.indexListedModels(itemsMap)

	if len(recentItems) > 0 && m.restrictTo == "" {
		recentGroup := NewModelGroup(t, "Recently used", false)

		// Recomputed from scratch: this runs again when the catalog
		// lands, and appending to the previous verdict would double up.
		m.staleRecents = nil
		for _, recent := range recentItems {
			key := modelKey(recent.Provider, recent.Model)
			item, ok := itemsMap[key]
			if !ok {
				// Before the catalog lands the item list is only the
				// configured providers, so a miss means "not known
				// yet", not "gone".
				if m.catalogLoaded {
					m.staleRecents = append(m.staleRecents, recent)
				}
				continue
			}

			// Show provider for recent items
			item = NewModelItem(t, item.prov, item.model, m.modelName, true)
			item.showProvider = true

			recentGroup.AppendItems(item)
			if recent.Model == currentModel.Model && recent.Provider == currentModel.Provider {
				selectedItemID = item.ID()
			}
		}

		if len(recentGroup.Items) > 0 {
			groups = append([]ModelGroup{recentGroup}, groups...)
		}
	}

	// Set model groups in the list.
	m.list.SetGroups(groups...)
	// The catalog can land after the user has typed, so a custom entry
	// built against the earlier, shorter list is re-judged here.
	m.syncCustomItem(m.input.Value())
	m.list.SetSelectedItem(selectedItemID)
	if selectedItemID != "" {
		m.list.ScrollToSelected()
	} else {
		m.list.ScrollToTop()
	}

	// Update placeholder based on model type
	if !m.isOnboarding {
		m.input.Placeholder = modelInputPlaceholder
	}
}

func modelKey(providerID, modelID string) string {
	if providerID == "" || modelID == "" {
		return ""
	}
	return providerID + ":" + modelID
}

// indexListedModels records the provider a custom entry belongs to. Only
// a restricted list has an unambiguous one.
func (m *Models) indexListedModels(items map[string]*ModelItem) {
	m.restrictedProvider = catwalk.Provider{}
	for _, item := range items {
		if item.prov.ID == m.restrictTo {
			m.restrictedProvider = item.prov
			return
		}
	}
}

// syncCustomItem offers whatever the user typed as a selectable model
// when nothing in the catalog matches it. It runs after the filter has
// been applied, because "matches nothing" is the filter's verdict — the
// list filters on model names, so an exact model ID surfaces no row of
// its own and would otherwise be a dead end.
//
// It is limited to onboarding because that is the only flow that goes on
// to register the model under its provider; elsewhere an unregistered
// model does not survive a config reload.
func (m *Models) syncCustomItem(query string) {
	id := strings.TrimSpace(query)
	if !m.isOnboarding || m.restrictTo == "" || id == "" || m.list.HasMatches() {
		m.list.SetCustom(nil)
		return
	}

	if current := m.list.Custom(); current != nil && current.model.ID == id {
		return
	}

	prov := m.restrictedProvider
	if prov.ID == "" {
		prov = catwalk.Provider{ID: m.restrictTo}
	}
	model := catwalk.Model{ID: id, Name: id}
	m.list.SetCustom(NewModelItem(m.com.Styles, prov, model, m.modelName, false))
}
