package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/completions"
	"github.com/stretchr/testify/require"
)

func mentionConfig() *config.Config {
	return &config.Config{
		Options: &config.Options{TUI: &config.TUIOptions{}},
		Agents: map[string]config.Agent{
			"coder":         {ID: "coder", Mode: config.AgentModePrimary},
			"explore":       {ID: "explore", Mode: config.AgentModeSubagent},
			"general":       {ID: "general", Mode: config.AgentModeSubagent},
			"plan":          {ID: "plan", Mode: config.AgentModeBranch},
			"deep-research": {ID: "deep-research", Mode: config.AgentModeBranch},
			"title":         {ID: "title", Mode: config.AgentModeSubagent, Hidden: ptrTo(true)},
		},
	}
}

func ptrTo[T any](v T) *T { return &v }

func agentIDs(values []completions.AgentCompletionValue) []string {
	ids := make([]string, len(values))
	for i, v := range values {
		ids[i] = v.ID
	}
	return ids
}

// TestMentionableAgentsExcludesPrimaryAndHidden pins who the popup offers.
// The mention exists to ask the coder to dispatch something, so naming the
// coder itself is a dead hint, and hidden agents back Angela's own internal
// calls rather than delegation.
func TestMentionableAgentsExcludesPrimaryAndHidden(t *testing.T) {
	t.Parallel()

	m := newTestUIWithConfig(t, mentionConfig())

	require.Equal(t,
		[]string{"deep-research", "explore", "general", "plan"},
		agentIDs(m.mentionableAgents()),
		"sorted by id so the popup order is stable across openings")
}

// TestMentionableAgentsWithoutConfig covers the startup window before a
// config is loaded, which the UI tolerates elsewhere too.
func TestMentionableAgentsWithoutConfig(t *testing.T) {
	t.Parallel()

	m := newTestUIWithConfig(t, nil)
	require.Empty(t, m.mentionableAgents())
}

func newMentionUI(cfg *config.Config) *UI {
	m := newBusyUI(&countingWorkspace{ready: true, cfg: cfg})
	m.textarea.Focus()
	m.textarea.SetWidth(60)
	return m
}

func typeKeys(m *UI, keys ...string) {
	for _, k := range keys {
		m.Update(tea.KeyPressMsg{Code: []rune(k)[0], Text: k})
	}
}

// TestAtOpensAgentCompletions walks the trigger the way a user does: the key
// goes through Update, not through a direct call, because the open decision
// and the query extraction live in two different places in that method.
func TestAtOpensAgentCompletions(t *testing.T) {
	pinTTLs(t)

	m := newMentionUI(mentionConfig())
	typeKeys(m, "@")

	require.True(t, m.completionsOpen)
	require.Equal(t, "@", m.completionsTrigger)
	require.True(t, m.completions.HasItems())

	typeKeys(m, "p", "l")
	require.Equal(t, "pl", m.completionsQuery)
}

// TestHashOpensFileCompletions pins that the file popup kept working after
// moving off "@" — it still arms on the new trigger and still loads async.
func TestHashOpensFileCompletions(t *testing.T) {
	pinTTLs(t)

	m := newMentionUI(mentionConfig())
	typeKeys(m, "#")

	require.True(t, m.completionsOpen)
	require.Equal(t, "#", m.completionsTrigger)
}

// TestTriggerOnlyFiresAtWordStart keeps an email address or a Go build tag
// from opening a popup mid-word.
func TestTriggerOnlyFiresAtWordStart(t *testing.T) {
	pinTTLs(t)

	for _, trigger := range []string{"@", "#"} {
		t.Run(trigger, func(t *testing.T) {
			m := newMentionUI(mentionConfig())
			typeKeys(m, "a", trigger)
			require.False(t, m.completionsOpen)
		})
	}
}

// TestAtWithNoAgentsDoesNotOpen is the guard against a popup that can never
// resolve. An open popup consumes Enter, so opening it empty would strand
// the user's message with no way to send it.
func TestAtWithNoAgentsDoesNotOpen(t *testing.T) {
	pinTTLs(t)

	cfg := mentionConfig()
	cfg.Agents = map[string]config.Agent{
		"coder": {ID: "coder", Mode: config.AgentModePrimary},
	}

	m := newMentionUI(cfg)
	typeKeys(m, "@")

	require.False(t, m.completionsOpen, "an empty popup would swallow Enter")
	require.Equal(t, "@", m.textarea.Value(), "the character still reaches the editor")
}

// TestInsertAgentCompletionKeepsSigil pins the difference between the two
// insert paths. A file's meaning rides along as an attachment, so its path
// goes in bare; a mention has nothing but its text, so the "@" has to
// survive.
func TestInsertAgentCompletionKeepsSigil(t *testing.T) {
	pinTTLs(t)

	t.Run("agent keeps it", func(t *testing.T) {
		m := newMentionUI(mentionConfig())
		m.textarea.SetValue("check @ex")
		m.completionsStartIndex = 6
		m.insertAgentCompletion("explore")
		require.Equal(t, "check @explore ", m.textarea.Value())
	})

	t.Run("file drops it", func(t *testing.T) {
		m := newMentionUI(mentionConfig())
		m.textarea.SetValue("check #ma")
		m.completionsStartIndex = 6
		require.True(t, m.insertCompletionText("main.go"))
		require.Equal(t, "check main.go ", m.textarea.Value())
	})
}

// TestMentionKeyBindings pins the help panel against the trigger swap: the
// two are wired independently, so a stale binding would advertise the old
// key without any test failing elsewhere.
func TestMentionKeyBindings(t *testing.T) {
	t.Parallel()

	km := DefaultKeyMap()
	require.Equal(t, []string{"@"}, km.Editor.MentionAgent.Keys())
	require.Equal(t, []string{"#"}, km.Editor.MentionFile.Keys())
}
