package dialog

import (
	"cmp"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/sahilm/fuzzy"
)

// providerConfiguredInfo is the trailing marker on providers that
// already hold credentials.
const providerConfiguredInfo = "Configured"

const (
	// addProviderItemID is the sentinel ID for the trailing "add custom
	// provider" row. Real provider IDs come from user input (validated
	// to look like a slug) or the catalog, so this double-underscored
	// form can never collide with one.
	addProviderItemID   = "__add_custom_provider__"
	addProviderItemName = "+ Add custom provider"
	addProviderItemInfo = "OpenAI-compatible"
)

// ProviderItem represents a selectable provider in the providers dialog.
type ProviderItem struct {
	*list.Versioned

	prov       catwalk.Provider
	configured bool

	cache   map[int]string
	t       *styles.Styles
	m       fuzzy.Match
	focused bool
}

var _ ListItem = &ProviderItem{}

// NewProviderItem creates a new ProviderItem.
func NewProviderItem(t *styles.Styles, prov catwalk.Provider, configured bool) *ProviderItem {
	return &ProviderItem{
		Versioned:  list.NewVersioned(),
		prov:       prov,
		configured: configured,
		t:          t,
		cache:      make(map[int]string),
	}
}

// Finished implements list.Item. Provider items are render-stable
// outside of explicit SetFocused / SetMatch.
func (p *ProviderItem) Finished() bool {
	return true
}

// Name is the provider's display name, falling back to its id.
func (p *ProviderItem) Name() string {
	return cmp.Or(p.prov.Name, string(p.prov.ID))
}

// Filter implements ListItem.
func (p *ProviderItem) Filter() string {
	return p.Name()
}

// ID implements ListItem.
func (p *ProviderItem) ID() string {
	return string(p.prov.ID)
}

// Render implements ListItem.
func (p *ProviderItem) Render(width int) string {
	var info string
	if p.configured {
		info = providerConfiguredInfo
	}
	sty := ListItemStyles{
		ItemBlurred:     p.t.Dialog.NormalItem,
		ItemFocused:     p.t.Dialog.SelectedItem,
		InfoTextBlurred: p.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: p.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(sty, p.Name(), info, p.focused, width, p.cache, &p.m)
}

// SetFocused implements ListItem.
func (p *ProviderItem) SetFocused(focused bool) {
	if p.focused == focused {
		return
	}
	p.cache = nil
	p.focused = focused
	if p.Versioned != nil {
		p.Bump()
	}
}

// SetMatch implements ListItem.
func (p *ProviderItem) SetMatch(fm fuzzy.Match) {
	if sameFuzzyMatch(p.m, fm) {
		return
	}
	p.cache = nil
	p.m = fm
	if p.Versioned != nil {
		p.Bump()
	}
}

// AddProviderItem is the trailing row, always present regardless of the
// search query, that lets the user configure a provider absent from
// both the catalog and their config instead of picking a catalog entry.
type AddProviderItem struct {
	*list.Versioned

	cache   map[int]string
	t       *styles.Styles
	m       fuzzy.Match
	focused bool
}

var _ ListItem = &AddProviderItem{}

// NewAddProviderItem creates a new AddProviderItem.
func NewAddProviderItem(t *styles.Styles) *AddProviderItem {
	return &AddProviderItem{
		Versioned: list.NewVersioned(),
		t:         t,
		cache:     make(map[int]string),
	}
}

// Finished implements list.Item. Render-stable outside of explicit
// SetFocused / SetMatch.
func (p *AddProviderItem) Finished() bool {
	return true
}

// Filter implements ListItem. It never actually runs through the fuzzy
// matcher — the row is appended after filtering — but the interface
// still requires it.
func (p *AddProviderItem) Filter() string {
	return addProviderItemName
}

// ID implements ListItem.
func (p *AddProviderItem) ID() string {
	return addProviderItemID
}

// Render implements ListItem.
func (p *AddProviderItem) Render(width int) string {
	sty := ListItemStyles{
		ItemBlurred:     p.t.Dialog.NormalItem,
		ItemFocused:     p.t.Dialog.SelectedItem,
		InfoTextBlurred: p.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: p.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(sty, addProviderItemName, addProviderItemInfo, p.focused, width, p.cache, &p.m)
}

// SetFocused implements ListItem.
func (p *AddProviderItem) SetFocused(focused bool) {
	if p.focused == focused {
		return
	}
	p.cache = nil
	p.focused = focused
	if p.Versioned != nil {
		p.Bump()
	}
}

// SetMatch implements ListItem.
func (p *AddProviderItem) SetMatch(fm fuzzy.Match) {
	if sameFuzzyMatch(p.m, fm) {
		return
	}
	p.cache = nil
	p.m = fm
	if p.Versioned != nil {
		p.Bump()
	}
}
