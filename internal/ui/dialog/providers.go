package dialog

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/util"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/sahilm/fuzzy"
)

// ProvidersCatalogMsg carries the provider catalog to the providers
// dialog once it has been fetched off the Update goroutine.
type ProvidersCatalogMsg struct {
	Providers []catwalk.Provider
}

// ProvidersID is the identifier for the provider selection dialog.
const ProvidersID = "providers"

const (
	onboardingProviderInputPlaceholder = "Find your provider"
	providerInputPlaceholder           = "Choose a provider"
)

// Providers is the first step of onboarding: it picks the provider whose
// credentials and models the following steps deal with.
type Providers struct {
	com          *common.Common
	isOnboarding bool

	providers []catwalk.Provider
	items     []*ProviderItem
	addItem   *AddProviderItem

	// catalogLoaded reports whether the catalog arrived. Until it does
	// the dialog lists the already-configured providers, so it always
	// opens with something selectable.
	catalogLoaded bool

	keyMap struct {
		UpDown   key.Binding
		Select   key.Binding
		Edit     key.Binding
		Next     key.Binding
		Previous key.Binding
		Close    key.Binding
	}
	list  *list.List
	input textinput.Model
	help  help.Model

	frame   *Frame
	metrics FrameMetrics
}

var _ Dialog = (*Providers)(nil)

// NewProviders creates a new Providers dialog. The provider catalog is
// not loaded here — see InitialCmd.
func NewProviders(com *common.Common, isOnboarding bool) *Providers {
	t := com.Styles
	m := &Providers{}
	m.com = com
	m.isOnboarding = isOnboarding

	m.frame = NewFrame(t, FrameSpec{
		Title:     "Choose Provider",
		MaxWidth:  defaultModelsDialogMaxWidth,
		MaxHeight: defaultDialogHeight,
	})

	h := help.New()
	h.Styles = t.DialogHelpStyles()
	m.help = h

	m.list = list.NewList()
	m.list.RegisterRenderCallback(list.FocusedRenderCallback(m.list))
	m.list.Focus()

	m.addItem = NewAddProviderItem(t)

	m.input = textinput.New()
	m.input.SetVirtualCursor(false)
	m.input.Placeholder = onboardingProviderInputPlaceholder
	if !isOnboarding {
		m.input.Placeholder = providerInputPlaceholder
	}
	m.input.SetStyles(t.TextInput)
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
func (m *Providers) ID() string {
	return ProvidersID
}

// InitialCmd fetches the provider catalog off the Update goroutine.
// Listing providers can block for as long as a catalog refresh takes,
// which must never happen on the render loop.
func (m *Providers) InitialCmd() tea.Cmd {
	cfg := m.com.Config()
	return func() tea.Msg {
		providers, err := config.Providers(cfg)
		if err != nil {
			// A stale catalog must not keep this dialog from working:
			// during onboarding it is the only way in.
			if len(providers) == 0 {
				return util.ReportError(fmt.Errorf("failed to get providers: %w", err))()
			}
			slog.Warn("Listing the previously known providers", "error", err)
		}
		return ProvidersCatalogMsg{Providers: providers}
	}
}

// SetProviders installs the fetched catalog and rebuilds the list.
func (m *Providers) SetProviders(providers []catwalk.Provider) {
	m.providers = providers
	m.catalogLoaded = true
	m.setProviderItems()
}

// setProviderItems rebuilds the item list from the configured providers
// merged with the catalog.
func (m *Providers) setProviderItems() {
	t := m.com.Styles
	cfg := m.com.Config()

	// decided holds every provider id already accounted for, whether it
	// became an item or was dropped for being disabled.
	decided := make(map[string]bool)
	var configured []*ProviderItem

	// Configured providers come first and are always present: the
	// catalog may not list them at all (a user-defined endpoint) and it
	// may still be in flight.
	for id, p := range cfg.Providers.Seq2() {
		decided[id] = true
		if p.Disable {
			continue
		}
		configured = append(configured, NewProviderItem(t, p.ToProvider(), true))
	}
	// Map iteration order is unstable, so the configured block would
	// otherwise reshuffle on every rebuild.
	sort.SliceStable(configured, func(i, j int) bool {
		return configured[i].Name() < configured[j].Name()
	})

	items := configured
	for _, provider := range m.providers {
		if decided[string(provider.ID)] {
			continue
		}
		items = append(items, NewProviderItem(t, provider, false))
	}
	m.items = items

	m.list.SetItems(m.visibleItems()...)
	m.selectCurrentProvider()
}

// selectCurrentProvider opens the list on the provider serving the main
// model, so confirming is a no-op rather than a silent switch.
func (m *Providers) selectCurrentProvider() {
	current := m.com.Config().Models[config.ModelMain].Provider
	if current != "" {
		for i, item := range m.items {
			if item.ID() == current {
				m.list.SetSelected(i)
				m.list.ScrollToSelected()
				return
			}
		}
	}
	m.list.SelectFirst()
	m.list.ScrollToTop()
}

// visibleItems returns the items matching the filter query. Every entry
// is selectable — there are no group headers or spacers to step over.
func (m *Providers) visibleItems() []list.Item {
	query := strings.ToLower(strings.ReplaceAll(m.input.Value(), " ", ""))

	// The add-custom-provider row is not fuzzy-matched: it is the way
	// out when the query matches nothing, so it must survive filtering
	// unconditionally rather than disappearing along with every other
	// row.
	if query == "" {
		items := make([]list.Item, 0, len(m.items)+1)
		for _, item := range m.items {
			item.SetMatch(fuzzy.Match{})
			items = append(items, item)
		}
		return append(items, m.addItem)
	}

	names := make([]string, len(m.items))
	for i, item := range m.items {
		names[i] = item.Filter()
	}

	matches := fuzzy.Find(query, names)
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].Index < matches[j].Index
	})

	items := make([]list.Item, 0, len(matches)+1)
	for _, match := range matches {
		item := m.items[match.Index]
		item.SetMatch(match)
		items = append(items, item)
	}
	return append(items, m.addItem)
}

// SelectedProvider returns the highlighted provider item, or nil when
// the list is empty.
func (m *Providers) SelectedProvider() *ProviderItem {
	item, _ := m.list.SelectedItem().(*ProviderItem)
	return item
}

// HandleMsg implements Dialog.
func (m *Providers) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, m.keyMap.Previous):
			m.list.Focus()
			if m.list.Selected() <= 0 {
				m.list.SelectLast()
			} else {
				m.list.SelectPrev()
			}
			m.list.ScrollToSelected()
		case key.Matches(msg, m.keyMap.Next):
			m.list.Focus()
			if m.list.Selected() >= m.list.Len()-1 {
				m.list.SelectFirst()
			} else {
				m.list.SelectNext()
			}
			m.list.ScrollToSelected()
		case key.Matches(msg, m.keyMap.Select, m.keyMap.Edit):
			switch item := m.list.SelectedItem().(type) {
			case *ProviderItem:
				return ActionSelectProvider{
					Provider:       item.prov,
					Configured:     item.configured,
					ReAuthenticate: key.Matches(msg, m.keyMap.Edit),
				}
			case *AddProviderItem:
				// Edit has nothing to reauthenticate here — the row has
				// no credentials of its own yet.
				if !key.Matches(msg, m.keyMap.Edit) {
					return ActionAddCustomProvider{Catalog: m.providers}
				}
			}
		default:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			m.list.Focus()
			m.list.SetItems(m.visibleItems()...)
			m.list.SelectFirst()
			m.list.ScrollToTop()
			return ActionCmd{cmd}
		}
	}
	return nil
}

// Cursor returns the cursor for the dialog.
func (m *Providers) Cursor() *tea.Cursor {
	return InputCursor(m.com.Styles, m.input.Cursor())
}

// Draw implements [Dialog].
func (m *Providers) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := m.com.Styles
	m.metrics = m.frame.Measure(area)
	m.input.SetWidth(m.frame.InputTextWidth(m.input, m.metrics.ContentWidth))

	listHeight, listTotalHeight, _ := m.frame.SizeList(m.list, m.metrics)

	rc := m.frame.Context(m.metrics)

	if m.isOnboarding {
		rc.AddPart(t.Dialog.PrimaryText.Render("To start, let's choose a provider."))
	}

	rc.AddPart(t.Dialog.InputPrompt.Render(m.input.View()))

	if !m.catalogLoaded && m.list.Len() == 0 {
		rc.AddPart(t.Dialog.SecondaryText.Render("Loading providers…"))
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
func (m *Providers) ShortHelp() []key.Binding {
	h := []key.Binding{
		m.keyMap.UpDown,
		m.keyMap.Select,
	}
	if item := m.SelectedProvider(); item != nil && item.configured {
		h = append(h, m.keyMap.Edit)
	}
	if !m.isOnboarding {
		h = append(h, m.keyMap.Close)
	}
	return h
}

// FullHelp returns the full help view.
func (m *Providers) FullHelp() [][]key.Binding {
	return [][]key.Binding{m.ShortHelp()}
}
