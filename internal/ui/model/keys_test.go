package model

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// displayOnlyBindings never reach a dispatch switch: they exist so the help
// line can show one hint for a pair of real bindings. Sharing keys with the
// bindings they summarize is the point, so they sit out the conflict check.
var displayOnlyBindings = map[string]bool{
	"Chat.UpDown":        true, // summarizes Chat.Up + Chat.Down
	"Chat.UpDownOneItem": true, // summarizes Chat.UpOneItem + Chat.DownOneItem
}

// layeredKeys are answered by more than one binding on purpose, with the
// handler order deciding which wins.
var layeredKeys = map[string]string{
	"esc":     "cancel a running turn first, then clear the selection",
	"alt+esc": "cancel a running turn first, then clear the selection",
}

// bindingsIn reflects over a keymap group and returns every binding keyed by
// the field path that declared it, so a conflict can name both culprits.
func bindingsIn(v reflect.Value, prefix string) map[string]key.Binding {
	out := map[string]key.Binding{}
	t := v.Type()
	for i := range t.NumField() {
		f, name := v.Field(i), prefix+t.Field(i).Name
		if f.Type() == reflect.TypeOf(key.Binding{}) {
			out[name] = f.Interface().(key.Binding)
			continue
		}
		if f.Kind() == reflect.Struct {
			for k, b := range bindingsIn(f, name+".") {
				out[k] = b
			}
		}
	}
	return out
}

// Two dispatchable bindings in one scope answering the same key means the
// earlier case in the handler silently wins and the later one looks broken.
func TestKeyBindingsDoNotCollideWithinAScope(t *testing.T) {
	t.Parallel()

	// Scope is the keymap group: Editor and Chat are dispatched under
	// different focus states, so they may share keys with each other and with
	// the globals. Within one group they may not.
	scopes := map[string]map[string]string{}
	for name, b := range bindingsIn(reflect.ValueOf(DefaultKeyMap()), "") {
		if displayOnlyBindings[name] {
			continue
		}
		scope, _, ok := strings.Cut(name, ".")
		if !ok {
			scope = "global"
		}
		if scopes[scope] == nil {
			scopes[scope] = map[string]string{}
		}
		for _, k := range b.Keys() {
			if _, layered := layeredKeys[k]; layered {
				continue
			}
			if prev, dup := scopes[scope][k]; dup {
				t.Errorf("scope %s: key %q bound by both %s and %s", scope, k, prev, name)
			}
			scopes[scope][k] = name
		}
	}
}

// ctrl+m is byte-identical to Enter on terminals without key disambiguation,
// so Models must keep a second key such terminals can actually send. Dropping
// it makes the models dialog unreachable there.
func TestModelsKeepsATerminalSafeAlias(t *testing.T) {
	t.Parallel()

	keys := DefaultKeyMap().Models.Keys()
	require.Contains(t, keys, "ctrl+m")
	require.Greater(t, len(keys), 1,
		"ctrl+m collides with enter where the terminal cannot disambiguate; "+
			"keep an alias (see the KeyboardEnhancementsMsg handler in ui.go)")
}

// The help line owns exactly one row. If it overflows, the status bar clips it
// and the user loses the hints at the end -- quit and more, the two they need
// when stuck. help.Model does not truncate reliably, so ShortHelp must fit.
func TestShortHelpFitsOneRow(t *testing.T) {
	pinTTLs(t)

	for _, width := range []int{60, 80, 100, 120, 200} {
		for _, state := range []uiState{uiLanding, uiChat} {
			for _, focus := range []uiFocusState{uiFocusEditor, uiFocusMain} {
				for _, busy := range []bool{false, true} {
					name := fmt.Sprintf("w=%d state=%d focus=%d busy=%v",
						width, state, focus, busy)

					m := drawableUI(t, width, 24)
					m.state = state
					m.focus = focus
					if state == uiChat {
						m.session = &session.Session{ID: "s1", Title: "t"}
					}
					m.agentBusyCache.set(busy)
					m.updateLayoutAndSize()

					binds := m.ShortHelp()
					require.LessOrEqual(t, len(binds), shortHelpLimit, name)

					got := m.status.help.View(m)
					require.NotContains(t, got, "\n", "%s: help must stay one row", name)
					require.LessOrEqual(t, ansi.StringWidth(got), m.helpWidth(),
						"%s: help overflows and gets clipped: %q", name, ansi.Strip(got))
					require.Contains(t, ansi.Strip(got), "ctrl+c",
						"%s: quit must survive trimming", name)
				}
			}
		}
	}
}

// FullHelp is the discovery surface: fixed groups so the columns do not
// reshuffle as session state changes, and none of them blank.
func TestFullHelpGroupsAreStable(t *testing.T) {
	pinTTLs(t)

	m := drawableUI(t, 140, 40)
	m.state = uiChat
	m.session = &session.Session{ID: "s1", Title: "t"}
	m.updateLayoutAndSize()

	groups := m.FullHelp()
	require.Len(t, groups, 4, "four fixed groups")
	for i, g := range groups {
		require.NotEmpty(t, g, "group %d must not be empty", i)
		for _, b := range g {
			require.NotEmpty(t, b.Help().Key, "group %d has a hint with no key", i)
		}
	}
}

// The pills panel and the sidebar are gone; nothing may still advertise a key
// that no handler answers.
func TestRetiredBindingsAreGone(t *testing.T) {
	t.Parallel()

	retired := []string{"ctrl+space"}
	for name, b := range bindingsIn(reflect.ValueOf(DefaultKeyMap()), "") {
		for _, k := range b.Keys() {
			require.NotContains(t, retired, k,
				"%s still binds %q, which belonged to the deleted pills panel", name, k)
		}
	}
}
