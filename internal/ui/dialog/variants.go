package dialog

import (
	"errors"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/ui/common"
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/sahilm/fuzzy"
)

const (
	// VariantsID is the identifier for the model variant dialog.
	VariantsID              = "variants"
	variantsDialogMaxWidth  = 50
	variantsDialogMinHeight = 8
	variantsDialogMaxHeight = 16

	// baselineVariantTitle names the entry that clears the preset. The
	// empty variant is a real choice, not the absence of one, so it
	// needs a label a user can read and select.
	baselineVariantTitle = "Baseline"
)

// Variants is a dialog for switching the parameter preset of the model
// the session already runs on. It changes no identity: same agent, same
// model, different request parameters.
type Variants struct {
	com   *common.Common
	help  help.Model
	list  *list.FilterableList
	input textinput.Model

	keyMap struct {
		Select   key.Binding
		Next     key.Binding
		Previous key.Binding
		UpDown   key.Binding
		Close    key.Binding
	}
}

// VariantItem represents one selectable preset.
type VariantItem struct {
	*list.Versioned
	variant   string
	title     string
	info      string
	isCurrent bool
	t         *styles.Styles
	m         fuzzy.Match
	cache     map[int]string
	focused   bool
}

// Finished implements list.Item. Variant items are render-stable
// outside of explicit SetFocused / SetMatch.
func (v *VariantItem) Finished() bool {
	return true
}

var (
	_ Dialog   = (*Variants)(nil)
	_ ListItem = (*VariantItem)(nil)
)

// NewVariants creates the variant selection dialog. The caller resolves
// which presets the session's model offers and which one is in effect,
// because that answer comes from the session record rather than config.
func NewVariants(com *common.Common, modelName string, variants []string, current string) (*Variants, error) {
	v := &Variants{com: com}

	h := help.New()
	h.Styles = com.Styles.DialogHelpStyles()
	v.help = h

	v.list = list.NewFilterableList()
	v.list.Focus()

	v.input = textinput.New()
	v.input.SetVirtualCursor(false)
	v.input.Placeholder = "Type to filter"
	v.input.SetStyles(com.Styles.TextInput)
	v.input.Focus()

	v.keyMap.Select = key.NewBinding(
		key.WithKeys("enter", "ctrl+y"),
		key.WithHelp("enter", "confirm"),
	)
	v.keyMap.Next = key.NewBinding(
		key.WithKeys("down", "ctrl+n"),
		key.WithHelp("↓", "next item"),
	)
	v.keyMap.Previous = key.NewBinding(
		key.WithKeys("up", "ctrl+p"),
		key.WithHelp("↑", "previous item"),
	)
	v.keyMap.UpDown = key.NewBinding(
		key.WithKeys("up", "down"),
		key.WithHelp("↑/↓", "choose"),
	)
	v.keyMap.Close = CloseKey

	if err := v.setVariantItems(modelName, variants, current); err != nil {
		return nil, err
	}

	return v, nil
}

// ID implements Dialog.
func (v *Variants) ID() string {
	return VariantsID
}

// HandleMsg implements [Dialog].
func (v *Variants) HandleMsg(msg tea.Msg) Action {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, v.keyMap.Close):
			return ActionClose{}
		case key.Matches(msg, v.keyMap.Previous):
			v.list.Focus()
			if v.list.IsSelectedFirst() {
				v.list.SelectLast()
				v.list.ScrollToBottom()
				break
			}
			v.list.SelectPrev()
			v.list.ScrollToSelected()
		case key.Matches(msg, v.keyMap.Next):
			v.list.Focus()
			if v.list.IsSelectedLast() {
				v.list.SelectFirst()
				v.list.ScrollToTop()
				break
			}
			v.list.SelectNext()
			v.list.ScrollToSelected()
		case key.Matches(msg, v.keyMap.Select):
			selectedItem := v.list.SelectedItem()
			if selectedItem == nil {
				break
			}
			variantItem, ok := selectedItem.(*VariantItem)
			if !ok {
				break
			}
			return ActionSelectVariant{Variant: variantItem.variant}
		default:
			var cmd tea.Cmd
			v.input, cmd = v.input.Update(msg)
			v.list.SetFilter(v.input.Value())
			v.list.ScrollToTop()
			v.list.SetSelected(0)
			return ActionCmd{cmd}
		}
	}
	return nil
}

// Cursor returns the cursor position relative to the dialog.
func (v *Variants) Cursor() *tea.Cursor {
	return InputCursor(v.com.Styles, v.input.Cursor())
}

// Draw implements [Dialog].
func (v *Variants) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	t := v.com.Styles
	width := max(0, min(variantsDialogMaxWidth, area.Dx()-t.Dialog.View.GetHorizontalBorderSize()))
	innerWidth := width - t.Dialog.View.GetHorizontalFrameSize()

	v.input.SetWidth(dialogInputTextWidth(t, v.input, innerWidth))

	listTotalHeight := v.list.TotalHeight()
	heightOffset := t.Dialog.Title.GetVerticalFrameSize() + titleContentHeight +
		t.Dialog.InputPrompt.GetVerticalFrameSize() + inputContentHeight +
		t.Dialog.HelpView.GetVerticalFrameSize() +
		t.Dialog.View.GetVerticalFrameSize()
	desiredHeight := heightOffset + listTotalHeight
	maxAvailable := area.Dy() - t.Dialog.View.GetVerticalBorderSize()
	height := max(variantsDialogMinHeight, min(variantsDialogMaxHeight, desiredHeight, maxAvailable))

	listHeight, listTotalHeight, _ := sizeDialogList(t, v.list, innerWidth, height)

	rc := NewRenderContext(t, width)
	rc.Title = "Select Variant"
	rc.AddPart(t.Dialog.InputPrompt.Render(v.input.View()))

	if v.list.Height() >= len(v.list.FilteredItems()) {
		v.list.ScrollToTop()
	} else {
		v.list.ScrollToSelected()
	}

	listView := t.Dialog.List.Height(v.list.Height()).Render(v.list.Render())
	listView = joinScrollbar(t, listView, listHeight, listTotalHeight, listHeight, v.list.Offset())
	rc.AddPart(listView)
	rc.Help = renderDialogHelp(t, &v.help, v, innerWidth)

	view := rc.Render()

	cur := v.Cursor()
	DrawCenterCursor(scr, area, view, cur)
	return cur
}

// ShortHelp implements [help.KeyMap].
func (v *Variants) ShortHelp() []key.Binding {
	return []key.Binding{
		v.keyMap.UpDown,
		v.keyMap.Select,
		v.keyMap.Close,
	}
}

// FullHelp implements [help.KeyMap].
func (v *Variants) FullHelp() [][]key.Binding {
	m := [][]key.Binding{}
	slice := []key.Binding{
		v.keyMap.Select,
		v.keyMap.Next,
		v.keyMap.Previous,
		v.keyMap.Close,
	}
	for i := 0; i < len(slice); i += 4 {
		end := min(i+4, len(slice))
		m = append(m, slice[i:end])
	}
	return m
}

func (v *Variants) setVariantItems(modelName string, variants []string, current string) error {
	if len(variants) == 0 {
		return errors.New("this model offers no variants")
	}

	items := make([]list.FilterableItem, 0, len(variants)+1)
	items = append(items, &VariantItem{
		Versioned: list.NewVersioned(),
		variant:   "",
		title:     baselineVariantTitle,
		info:      modelName,
		isCurrent: current == "",
		t:         v.com.Styles,
	})
	selectedIndex := 0
	for i, name := range variants {
		items = append(items, &VariantItem{
			Versioned: list.NewVersioned(),
			variant:   name,
			title:     name,
			isCurrent: name == current,
			t:         v.com.Styles,
		})
		if name == current {
			selectedIndex = i + 1
		}
	}

	v.list.SetItems(items...)
	v.list.SetSelected(selectedIndex)
	v.list.ScrollToSelected()
	return nil
}

// Filter returns the filter value for the variant item.
func (v *VariantItem) Filter() string {
	return v.title
}

// ID returns the unique identifier for the variant. The baseline has no
// name of its own, so it borrows its title to stay distinct in the list.
func (v *VariantItem) ID() string {
	if v.variant == "" {
		return baselineVariantTitle
	}
	return v.variant
}

// SetFocused sets the focus state of the variant item.
func (v *VariantItem) SetFocused(focused bool) {
	if v.focused == focused {
		return
	}
	v.cache = nil
	v.focused = focused
	if v.Versioned != nil {
		v.Bump()
	}
}

// SetMatch sets the fuzzy match for the variant item.
func (v *VariantItem) SetMatch(m fuzzy.Match) {
	if sameFuzzyMatch(v.m, m) {
		return
	}
	v.cache = nil
	v.m = m
	if v.Versioned != nil {
		v.Bump()
	}
}

// Render returns the string representation of the variant item.
func (v *VariantItem) Render(width int) string {
	info := v.info
	if v.isCurrent {
		info = "current"
	}
	itemStyles := ListItemStyles{
		ItemBlurred:     v.t.Dialog.NormalItem,
		ItemFocused:     v.t.Dialog.SelectedItem,
		InfoTextBlurred: v.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: v.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(itemStyles, v.title, info, v.focused, width, v.cache, &v.m)
}
