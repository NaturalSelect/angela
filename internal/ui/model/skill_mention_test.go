package model

import (
	"image"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/NaturalSelect/angela/internal/ui/completions"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/stretchr/testify/require"
)

func skillNames(values []completions.SkillCompletionValue) []string {
	names := make([]string, len(values))
	for i, v := range values {
		names[i] = v.Name
	}
	return names
}

// TestMentionableSkillsSortsByName pins the popup order to be stable
// across openings, independent of discovery order.
func TestMentionableSkillsSortsByName(t *testing.T) {
	t.Parallel()

	m := newMentionUI(t, mentionConfig())
	m.skillEntries = []skills.CatalogEntry{
		{Name: "jq", Description: "d1"},
		{Name: "angela-config", Description: "d2"},
	}

	require.Equal(t, []string{"angela-config", "jq"}, skillNames(m.mentionableSkills()))
}

// TestMentionableSkillsWithoutEntries covers the startup window before
// the skill catalog has loaded.
func TestMentionableSkillsWithoutEntries(t *testing.T) {
	t.Parallel()

	m := newMentionUI(t, mentionConfig())
	require.Empty(t, m.mentionableSkills())
}

// TestAmpersandOpensSkillCompletions walks the trigger the way a user
// does, mirroring TestAtOpensAgentCompletions for "@".
func TestAmpersandOpensSkillCompletions(t *testing.T) {
	pinTTLs(t)

	m := newMentionUI(t, mentionConfig())
	m.skillEntries = []skills.CatalogEntry{{Name: "jq", Description: "Query JSON"}}
	typeKeys(m, "&")

	require.True(t, m.completionsOpen)
	require.Equal(t, "&", m.completionsTrigger)
	require.True(t, m.completions.HasItems())

	typeKeys(m, "j")
	require.Equal(t, "j", m.completionsQuery)
}

// TestAmpersandWithNoSkillsDoesNotOpen mirrors
// TestAtWithNoAgentsDoesNotOpen: an empty popup would swallow Enter.
func TestAmpersandWithNoSkillsDoesNotOpen(t *testing.T) {
	pinTTLs(t)

	m := newMentionUI(t, mentionConfig())
	typeKeys(m, "&")

	require.False(t, m.completionsOpen, "an empty popup would swallow Enter")
	require.Equal(t, "&", m.textarea.Value(), "the character still reaches the editor")
}

// TestAmpersandOnlyFiresAtWordStart matches the "@"/"#" guard in
// TestTriggerOnlyFiresAtWordStart.
func TestAmpersandOnlyFiresAtWordStart(t *testing.T) {
	pinTTLs(t)

	m := newMentionUI(t, mentionConfig())
	m.skillEntries = []skills.CatalogEntry{{Name: "jq"}}
	typeKeys(m, "a", "&")

	require.False(t, m.completionsOpen)
}

// TestInsertSkillCompletionInsertsBracketToken pins the exact mention
// syntax: "[skill:name]" text, not the trigger character.
func TestInsertSkillCompletionInsertsBracketToken(t *testing.T) {
	pinTTLs(t)

	m := newMentionUI(t, mentionConfig())
	m.textarea.SetValue("use &j")
	m.completionsStartIndex = 4
	m.insertSkillCompletion("jq")
	require.Equal(t, "use [skill:jq] ", m.textarea.Value())
}

func TestSkillMentionKeyBinding(t *testing.T) {
	t.Parallel()

	km := DefaultKeyMap()
	require.Equal(t, []string{"&"}, km.Editor.MentionSkill.Keys())
}

// TestMouseClickOnCompletionsPopupRespectsReverseRendering is a
// regression guard for the popup's bottom-to-top rendering
// (completions.New calls SetReverse(true)): the top screen row shows
// the last item passed to SetSkills, and list.ItemIndexAtPosition walks
// items in that un-reversed order, so a click has to flip the screen
// row before resolving which item it landed on. Getting the flip
// backwards silently swaps every row for its mirror, which a test that
// only checks the middle row would miss.
func TestMouseClickOnCompletionsPopupRespectsReverseRendering(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	skills := []completions.SkillCompletionValue{{Name: "first"}, {Name: "second"}, {Name: "third"}}

	m.completions.SetSkills(skills)
	m.completionsOpen = true
	m.completionsRect = image.Rectangle{Min: image.Pt(0, 0), Max: image.Pt(20, 3)}
	m.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: uv.MouseLeft})
	require.Equal(t, "[skill:third] ", m.textarea.Value(), "the top screen row must resolve to the last item")

	m.textarea.SetValue("")
	m.completions.SetSkills(skills)
	m.completionsOpen = true
	m.completionsRect = image.Rectangle{Min: image.Pt(0, 0), Max: image.Pt(20, 3)}
	m.Update(tea.MouseClickMsg{X: 0, Y: 2, Button: uv.MouseLeft})
	require.Equal(t, "[skill:first] ", m.textarea.Value(), "the bottom screen row must resolve to the first item")
}

// TestMouseClickOutsideCompletionsPopupIsIgnored guards against treating
// every click as a popup selection while it happens to be open.
func TestMouseClickOutsideCompletionsPopupIsIgnored(t *testing.T) {
	t.Parallel()

	m, _ := newMockBusyUI(t)
	m.completions.SetSkills([]completions.SkillCompletionValue{{Name: "jq"}})
	m.completionsOpen = true
	m.completionsRect = image.Rectangle{Min: image.Pt(0, 0), Max: image.Pt(20, 1)}

	m.Update(tea.MouseClickMsg{X: 50, Y: 50, Button: uv.MouseLeft})

	require.Empty(t, m.textarea.Value())
	require.True(t, m.completionsOpen, "a miss must not close the popup")
}
