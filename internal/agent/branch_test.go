package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderBranchForkPromptCarriesTheTask(t *testing.T) {
	t.Parallel()

	out, err := renderBranchForkPrompt(branchForkPrompt{
		ParentTitle: "Refactor the auth layer",
		Prompt:      "Work out with me which sessions to invalidate on password change.",
	})
	require.NoError(t, err)

	require.Contains(t, out, "Refactor the auth layer")
	require.Contains(t, out, "Work out with me which sessions to invalidate")
}

func TestRenderBranchForkPromptWithoutAParentTitle(t *testing.T) {
	t.Parallel()

	out, err := renderBranchForkPrompt(branchForkPrompt{Prompt: "do the thing"})
	require.NoError(t, err)

	require.NotContains(t, out, `""`, "an empty title left dangling quotes in the prompt")
	require.Contains(t, out, "the conversation above")
	require.Contains(t, out, "do the thing")
}

// flatten collapses the templates' hard wrapping so a phrase check does not
// depend on where a line happens to break.
func flatten(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// The preamble is the only part of a branch's system prompt Angela controls:
// the rest is whatever the user configured, and may say nothing about any of
// this. If a rule drifts out, a branch either never finishes or gives up the
// first time the user pushes back, and the suspended parent hangs either way.
func TestBranchPreambleStatesTheContract(t *testing.T) {
	t.Parallel()

	flat := flatten(string(branchPreambleTmpl))

	for _, tc := range []struct{ need, why string }{
		{"merge", "nothing names the tool that finishes the branch"},
		{"reject", "nothing says a merge can be turned down"},
		{"no way to abandon", "nothing says the model cannot end the branch itself"},
		{"suspended", "nothing says the parent is blocked on this branch"},
		{"directly", "nothing says the model is talking to the user rather than an agent"},
	} {
		require.Contains(t, flat, tc.need, tc.why)
	}
}

// The fork prompt is a user message landing after the copied history, so it
// owns the two things only that position can state.
func TestForkPromptCarriesInheritanceAndTask(t *testing.T) {
	t.Parallel()

	out, err := renderBranchForkPrompt(branchForkPrompt{
		ParentTitle: "Refactor the auth layer",
		Prompt:      "Work out with me which sessions to invalidate.",
	})
	require.NoError(t, err)

	require.Contains(t, out, "Refactor the auth layer")
	require.Contains(t, out, "Work out with me which sessions to invalidate")
	require.Contains(t, flatten(out), "history",
		"nothing tells the model the transcript above is inherited")
}
