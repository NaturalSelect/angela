package dialog

import (
	"github.com/NaturalSelect/angela/internal/ui/list"
	"github.com/NaturalSelect/angela/internal/ui/styles"
)

const setEnvNewLabel = "+ New variable"

// EnvVarItem is one row of the environment variable list: a variable set
// this session, or the row that starts a new one. It holds the name only,
// never the value.
type EnvVarItem struct {
	*list.Versioned
	name    string
	t       *styles.Styles
	cache   map[int]string
	focused bool
}

var (
	_ list.FilterableItem = (*EnvVarItem)(nil)
	_ list.Focusable      = (*EnvVarItem)(nil)
)

// NewEnvVarItem creates a row for a variable set this session. An empty
// name makes the row that starts a new variable.
func NewEnvVarItem(t *styles.Styles, name string) *EnvVarItem {
	return &EnvVarItem{
		Versioned: list.NewVersioned(),
		name:      name,
		t:         t,
		cache:     make(map[int]string),
	}
}

// Name returns the variable name, or "" for the row that adds a variable.
func (i *EnvVarItem) Name() string {
	return i.name
}

// IsNew reports whether this is the row that adds a variable.
func (i *EnvVarItem) IsNew() bool {
	return i.name == ""
}

// Finished implements list.Item. A row only changes through SetFocused,
// which bumps the version.
func (i *EnvVarItem) Finished() bool {
	return true
}

// Filter implements list.FilterableItem.
func (i *EnvVarItem) Filter() string {
	return i.name
}

// SetFocused implements list.Focusable.
func (i *EnvVarItem) SetFocused(focused bool) {
	if i.focused == focused {
		return
	}
	i.cache = make(map[int]string)
	i.focused = focused
	i.Bump()
}

// Render implements list.Item.
func (i *EnvVarItem) Render(width int) string {
	title := i.name
	if i.IsNew() {
		title = setEnvNewLabel
	}
	itemStyles := ListItemStyles{
		ItemBlurred:     i.t.Dialog.NormalItem,
		ItemFocused:     i.t.Dialog.SelectedItem,
		InfoTextBlurred: i.t.Dialog.ListItem.InfoBlurred,
		InfoTextFocused: i.t.Dialog.ListItem.InfoFocused,
	}
	return renderItem(itemStyles, title, "", i.focused, width, i.cache, nil)
}
