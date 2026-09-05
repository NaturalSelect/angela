package model

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/ui/completions"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
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

func newMentionUI(t *testing.T, cfg *config.Config) *UI {
	t.Helper()

	ctrl := gomock.NewController(t)
	ws := NewMockWorkspace(ctrl)
	ws.EXPECT().AgentIsReady().Return(true).AnyTimes()
	ws.EXPECT().Config().Return(cfg).AnyTimes()
	ws.EXPECT().WorkingDir().Return("").AnyTimes()

	m := newBusyUIWithWorkspace(ws)
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

	m := newMentionUI(t, mentionConfig())
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

	m := newMentionUI(t, mentionConfig())
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
			m := newMentionUI(t, mentionConfig())
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

	m := newMentionUI(t, cfg)
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
		m := newMentionUI(t, mentionConfig())
		m.textarea.SetValue("check @ex")
		m.completionsStartIndex = 6
		m.insertAgentCompletion("explore")
		require.Equal(t, "check @explore ", m.textarea.Value())
	})

	t.Run("file drops it", func(t *testing.T) {
		m := newMentionUI(t, mentionConfig())
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

// TestApplyCompletionSelection_RoutesEachValueType pins every branch of
// the switch: each known SelectionMsg type must reach its own insert*
// helper (not fall through to a neighbor's), report the KeepOpen the
// selection carried, and mark handled so the caller closes the popup
// or not accordingly. A message the switch does not recognize must
// report handled=false so callers leave it alone instead of treating
// it as a no-op selection.
func TestApplyCompletionSelection_RoutesEachValueType(t *testing.T) {
	t.Parallel()

	t.Run("file", func(t *testing.T) {
		t.Parallel()
		m := newMentionUI(t, mentionConfig())
		cmd, keepOpen, handled := m.applyCompletionSelection(completions.SelectionMsg[completions.FileCompletionValue]{
			Value:    completions.FileCompletionValue{Path: "main.go"},
			KeepOpen: false,
		})
		require.True(t, handled)
		require.False(t, keepOpen)
		require.NotNil(t, cmd, "a file selection always carries an attachment command")
		require.Equal(t, "main.go ", m.textarea.Value())
	})

	t.Run("resource", func(t *testing.T) {
		t.Parallel()
		m := newMentionUI(t, mentionConfig())
		cmd, keepOpen, handled := m.applyCompletionSelection(completions.SelectionMsg[completions.ResourceCompletionValue]{
			Value:    completions.ResourceCompletionValue{MCPName: "srv", URI: "file:///a", Title: "A"},
			KeepOpen: true,
		})
		require.True(t, handled)
		require.True(t, keepOpen)
		require.NotNil(t, cmd, "a resource selection always carries a fetch command")
		require.Equal(t, "A ", m.textarea.Value())
	})

	t.Run("agent", func(t *testing.T) {
		t.Parallel()
		m := newMentionUI(t, mentionConfig())
		cmd, keepOpen, handled := m.applyCompletionSelection(completions.SelectionMsg[completions.AgentCompletionValue]{
			Value:    completions.AgentCompletionValue{ID: "explore"},
			KeepOpen: false,
		})
		require.True(t, handled)
		require.False(t, keepOpen)
		require.Nil(t, cmd, "an agent mention has no height change to report from an empty textarea")
		require.Equal(t, "@explore ", m.textarea.Value())
	})

	t.Run("skill", func(t *testing.T) {
		t.Parallel()
		m := newMentionUI(t, mentionConfig())
		cmd, keepOpen, handled := m.applyCompletionSelection(completions.SelectionMsg[completions.SkillCompletionValue]{
			Value:    completions.SkillCompletionValue{Name: "jq"},
			KeepOpen: false,
		})
		require.True(t, handled)
		require.False(t, keepOpen)
		require.Nil(t, cmd, "a skill mention has no height change to report from an empty textarea")
		require.Equal(t, "[skill:jq] ", m.textarea.Value())
	})

	t.Run("unrecognized message", func(t *testing.T) {
		t.Parallel()
		m := newMentionUI(t, mentionConfig())
		cmd, keepOpen, handled := m.applyCompletionSelection(completions.ClosedMsg{})
		require.False(t, handled, "an unrecognized message must not be treated as a selection")
		require.False(t, keepOpen)
		require.Nil(t, cmd)
	})
}
