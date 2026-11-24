package agent

import (
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/stretchr/testify/require"
)

// TestGenerateChangeDescriptionRejectsEmptyChanges pins that blank input
// never reaches agent resolution: the zero-value coordinator has no config
// or sessions, so only a check that runs first can produce this error.
func TestGenerateChangeDescriptionRejectsEmptyChanges(t *testing.T) {
	t.Parallel()

	coord := &coordinator{}
	got, err := coord.GenerateChangeDescription(t.Context(), "sess", "main", "feature", "  \n\t")
	require.Empty(t, got)
	require.ErrorContains(t, err, "no changes to describe")
}

// TestGenerateChangeDescriptionFailsWhenDescribeAgentIsNotConfigured pins
// that an unresolvable describe agent surfaces as a wrapped error instead
// of panicking or silently falling back.
func TestGenerateChangeDescriptionFailsWhenDescribeAgentIsNotConfigured(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := &coordinator{sessions: env.sessions, cfg: config.NewTestStore(&config.Config{
		Providers: csync.NewMap[string, config.ProviderConfig](),
		Options:   &config.Options{},
	})}

	got, err := coord.GenerateChangeDescription(t.Context(), "sess", "main", "feature", "Diff:\ndiff --git a/x b/x\n+added line")
	require.Empty(t, got)
	require.ErrorContains(t, err, "failed to resolve the describe agent")
}

// TestGenerateChangeDescriptionRejectsEmptyModelResponse pins that a model
// reply with nothing but whitespace is a failure, not an empty description
// handed back to the caller.
func TestGenerateChangeDescriptionRejectsEmptyModelResponse(t *testing.T) {
	t.Parallel()

	server := commitCompletionServer(t, "   ", nil)
	defer server.Close()

	coord := newModelPrefTestCoordinator(t, nil)
	setMockProviderBaseURL(t, coord, server.URL)

	got, err := coord.GenerateChangeDescription(t.Context(), "", "main", "feature", "Diff:\ndiff --git a/x b/x\n+added line")
	require.Empty(t, got)
	require.ErrorContains(t, err, "change description generation returned an empty description")
}

// TestGenerateChangeDescriptionNamesBothBranchesAndKeepsMarkdown pins that
// the model is told which branches are compared, and that the Markdown it
// returns survives intact, including inline code at the end of a line and a
// fence the model wrapped around the whole answer.
func TestGenerateChangeDescriptionNamesBothBranchesAndKeepsMarkdown(t *testing.T) {
	t.Parallel()

	const inner = "feat: add login flow\n\n## Summary\nAdds the `login` command."

	var gotBody []byte
	server := commitCompletionServer(t, "```markdown\n"+inner+"\n```", &gotBody)
	defer server.Close()

	coord := newModelPrefTestCoordinator(t, nil)
	setMockProviderBaseURL(t, coord, server.URL)

	got, err := coord.GenerateChangeDescription(t.Context(), "", "origin/main", "feature/login", "Diff:\ndiff --git a/x b/x\n+added line")
	require.NoError(t, err)
	require.Equal(t, inner, got)
	require.Contains(t, string(gotBody), "feature/login")
	require.Contains(t, string(gotBody), "origin/main")
}

// TestGenerateChangeDescriptionTruncatesTheTailOfTheChanges pins that an
// over-budget input is cut from the end, so the commit list and diffstat
// that lead it still reach the model.
func TestGenerateChangeDescriptionTruncatesTheTailOfTheChanges(t *testing.T) {
	t.Parallel()

	var gotBody []byte
	server := commitCompletionServer(t, "feat: add things", &gotBody)
	defer server.Close()

	coord := newModelPrefTestCoordinator(t, nil)
	setMockProviderBaseURL(t, coord, server.URL)
	coord.cfg.Overrides().CommitDiffRunes = new(120)

	changes := "Commits:\n- add the first thing\n\nDiff:\n" + strings.Repeat("+added line\n", 200)
	_, err := coord.GenerateChangeDescription(t.Context(), "", "main", "feature", changes)
	require.NoError(t, err)
	require.Contains(t, string(gotBody), "add the first thing")
	require.Contains(t, string(gotBody), "(diff truncated)")
	require.NotContains(t, string(gotBody), strings.Repeat("+added line\\n", 50),
		"the budget must cut the diff well below its full length")
}

// TestStripCodeFence pins that only a fence wrapping the whole text is
// removed.
func TestStripCodeFence(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct{ in, want string }{
		"plain text":           {"feat: x\n\nbody", "feat: x\n\nbody"},
		"surrounding space":    {"\n  feat: x  \n", "feat: x"},
		"fence with language":  {"```markdown\nfeat: x\n```", "feat: x"},
		"bare fence":           {"```\nfeat: x\nbody\n```", "feat: x\nbody"},
		"leading fence only":   {"```go\nfoo()\n```\nand more text", "```go\nfoo()\n```\nand more text"},
		"inline code at start": {"`a` and `b`", "`a` and `b`"},
		"one line of backtick": {"```", "```"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, stripCodeFence(tc.in))
		})
	}
}
