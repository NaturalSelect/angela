package agent

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent/prompt"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// TestAgentPrompt_SubagentTemplatesRenderContextFiles pins the fix for
// subagents ignoring the project's AGENTS.md. Only the coder template
// used to render the loaded context files, so a delegated agent worked
// without the conventions the user had written down — and the data was
// present on the prompt struct the whole time, which is why asserting
// on promptData never caught it.
func TestAgentPrompt_SubagentTemplatesRenderContextFiles(t *testing.T) {
	const marker = "always use tabs in this project"

	dir := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	t.Setenv("ANGELA_GLOBAL_DATA", globalDir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(marker), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"),
		[]byte(`{
			"options": {"disable_default_providers": true},
			"providers": {
				"test": {
					"base_url": "http://127.0.0.1:0/v1",
					"api_key": "test",
					"models": [{"id": "test-model", "name": "Test"}]
				}
			}
		}`), 0o644))

	store, err := config.Init(dir, "", false)
	require.NoError(t, err)

	for _, id := range []string{
		config.AgentCoder,
		config.AgentExplore,
		config.AgentGeneral,
		config.AgentPlan,
		config.AgentCoDesign,
		config.AgentDeepResearch,
	} {
		t.Run(id, func(t *testing.T) {
			agentCfg, ok := store.Config().Agents[id]
			require.True(t, ok)

			p, err := agentPrompt(agentCfg, prompt.WithWorkingDir(dir))
			require.NoError(t, err)

			out, err := p.Build(context.Background(), "", "", store)
			require.NoError(t, err)
			require.Contains(t, out, "<project_context>",
				"%s must render the project context section", id)
			require.Contains(t, out, marker,
				"%s must include the project's AGENTS.md content", id)
		})
	}
}

// TestAgentPrompt_UnknownAgentRendersContextFiles covers the fallback
// path: an agent ID with no built-in template lands on the general
// template, which must carry context too.
func TestAgentPrompt_UnknownAgentRendersContextFiles(t *testing.T) {
	const marker = "house rule: no global state"

	dir := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	t.Setenv("ANGELA_GLOBAL_DATA", globalDir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(marker), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"),
		[]byte(`{
			"options": {"disable_default_providers": true},
			"providers": {
				"test": {
					"base_url": "http://127.0.0.1:0/v1",
					"api_key": "test",
					"models": [{"id": "test-model", "name": "Test"}]
				}
			}
		}`), 0o644))

	store, err := config.Init(dir, "", false)
	require.NoError(t, err)

	p, err := agentPrompt(config.Agent{ID: "reviewer", Name: "Reviewer"}, prompt.WithWorkingDir(dir))
	require.NoError(t, err)

	out, err := p.Build(context.Background(), "", "", store)
	require.NoError(t, err)
	require.Contains(t, out, marker)
}

// TestAgentPrompt_CompactModeFallsBackToSummaryTemplate pins that a
// custom compact-mode agent without its own Prompt gets the
// summarization template rather than the general one. Such an agent
// typically exists only to run compaction on a different model or
// slot, and falling through to the general-assistant template would
// silently change what it does.
func TestAgentPrompt_CompactModeFallsBackToSummaryTemplate(t *testing.T) {
	dir := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	t.Setenv("ANGELA_GLOBAL_DATA", globalDir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"),
		[]byte(`{
			"options": {"disable_default_providers": true},
			"providers": {
				"test": {
					"base_url": "http://127.0.0.1:0/v1",
					"api_key": "test",
					"models": [{"id": "test-model", "name": "Test"}]
				}
			}
		}`), 0o644))

	store, err := config.Init(dir, "", false)
	require.NoError(t, err)

	p, err := agentPrompt(config.Agent{ID: "my-compact", Mode: config.AgentModeCompact}, prompt.WithWorkingDir(dir))
	require.NoError(t, err)

	out, err := p.Build(context.Background(), "", "", store)
	require.NoError(t, err)
	require.Contains(t, out, "You are summarizing a conversation to preserve context for continuing work later.")
	require.NotContains(t, out, "You are a general-purpose agent for Angela.")
}

// TestInitializePrompt_OverridableViaConfig pins step 2.8's whole
// point: initialize makes no LLM call of its own, so its prompt is the
// only thing it owns — and that prompt must be reachable through the
// normal agent config path rather than frozen in the binary.
func TestInitializePrompt_OverridableViaConfig(t *testing.T) {
	const marker = "CUSTOM INITIALIZE MARKER"

	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	t.Setenv("ANGELA_GLOBAL_DATA", globalDir)

	t.Run("override wins", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"),
			[]byte(`{
				"options": {"disable_default_providers": true},
				"providers": {"test": {"base_url": "http://127.0.0.1:0/v1", "api_key": "test",
					"models": [{"id": "test-model", "name": "Test"}]}},
				"agents": {"initialize": {"prompt": "`+marker+`"}}
			}`), 0o644))

		store, err := config.Init(dir, "", false)
		require.NoError(t, err)

		out, err := InitializePrompt(store)
		require.NoError(t, err)
		require.Contains(t, out, marker)
	})

	t.Run("built-in template by default", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"),
			[]byte(`{
				"options": {"disable_default_providers": true},
				"providers": {"test": {"base_url": "http://127.0.0.1:0/v1", "api_key": "test",
					"models": [{"id": "test-model", "name": "Test"}]}}
			}`), 0o644))

		store, err := config.Init(dir, "", false)
		require.NoError(t, err)

		out, err := InitializePrompt(store)
		require.NoError(t, err)
		require.NotContains(t, out, marker)
		require.NotEmpty(t, out)
	})
}

// TestAgentPrompt_BranchPreamble covers the three ways agentPrompt resolves a
// prompt. A branch runs on an agent the user wrote, so the preamble cannot
// live in any template: whichever way the body is resolved — and even when a
// custom prompt replaces the template outright — the branch rules have to
// still be there, and still be first.
func TestAgentPrompt_BranchPreamble(t *testing.T) {
	const customMarker = "REVIEW EVERY DIFF TWICE"

	dir := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	t.Setenv("ANGELA_GLOBAL_DATA", globalDir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"),
		[]byte(`{
			"options": {"disable_default_providers": true},
			"providers": {"test": {"base_url": "http://127.0.0.1:0/v1", "api_key": "test",
				"models": [{"id": "test-model", "name": "Test"}]}}
		}`), 0o644))

	store, err := config.Init(dir, "", false)
	require.NoError(t, err)

	preamble := strings.TrimRight(string(branchPreambleTmpl), "\n")

	build := func(t *testing.T, agentCfg config.Agent) string {
		t.Helper()
		p, err := agentPrompt(agentCfg, prompt.WithWorkingDir(dir))
		require.NoError(t, err)
		out, err := p.Build(context.Background(), "", "", store)
		require.NoError(t, err)
		return out
	}

	t.Run("custom prompt", func(t *testing.T) {
		out := build(t, config.Agent{
			ID: "pairing", Mode: config.AgentModeBranch, Prompt: customMarker,
		})
		require.True(t, strings.HasPrefix(out, preamble),
			"the branch rules must lead the prompt, not trail the user's")
		require.Contains(t, out, customMarker,
			"the preamble replaced the user's prompt instead of fronting it")
	})

	t.Run("built-in template", func(t *testing.T) {
		out := build(t, config.Agent{ID: config.AgentGeneral, Mode: config.AgentModeBranch})
		require.True(t, strings.HasPrefix(out, preamble))
	})

	t.Run("unknown agent falls back to general", func(t *testing.T) {
		out := build(t, config.Agent{ID: "nobody", Mode: config.AgentModeBranch})
		require.True(t, strings.HasPrefix(out, preamble))
	})

	t.Run("not a branch", func(t *testing.T) {
		out := build(t, config.Agent{
			ID: "pairing", Mode: config.AgentModeSubagent, Prompt: customMarker,
		})
		require.False(t, strings.HasPrefix(out, preamble),
			"an ordinary subagent was told it is a branch")
		require.Contains(t, out, customMarker)
	})
}

var blockquoteMarker = regexp.MustCompile(`(?m)^\s*> ?`)

// normalizedCoDesignTemplate flattens line wrapping and blockquote markers so
// assertions survive rewrapping the template's prose.
func normalizedCoDesignTemplate() string {
	unquoted := blockquoteMarker.ReplaceAllString(string(coDesignPromptTmpl), "")
	return strings.Join(strings.Fields(unquoted), " ")
}

// TestCoDesignPrompt_ForcedChangesAreConsequencesNotChoices pins that when
// an accepted decision A leaves only one viable form for another element B,
// co-design states B as a consequence instead of asking the user to pick B
// from options where only one holds. Each clause closes one path that used
// to push the model back into asking. Standing is said in plain words, so no
// marker survives to leak into the document.
func TestCoDesignPrompt_ForcedChangesAreConsequencesNotChoices(t *testing.T) {
	t.Parallel()

	tpl := normalizedCoDesignTemplate()

	for name, clause := range map[string]string{
		"rule shows forced change as a consequence": "it is a consequence, not a choice: present it in one sentence naming the decision and why each alternative fails.",
		"rule demands why each alternative fails":   "If you cannot say why each fails, it is a real choice: ask.",
		"objection reopens the forcing decision":    "An objection to a consequence reopens the decision that forced it.",
		"unseen rule admits consequences":           "saw it as a consequence and did not object",
		"step 3 traces before asking":               "Trace what the section touches elsewhere in the design",
		"to confirm needs a real choice":            "Ask only where more than one answer still holds",
		"consequences do not block merge":           "Each piece was shown here",
		"contract binds the accepted design":        "The design above is what the user accepted: a plan or implementation that has to deviate from it must stop and tell the user first.",
		"accepted pieces read as plain design":      "accepted suggestions and consequences read as plain design",
	} {
		require.Contains(t, tpl, clause, name)
	}
	for _, marker := range []string{"// suggested:", "// follows:", "to confirm"} {
		require.NotContains(t, tpl, marker)
	}
}

// TestCoDesignPrompt_VerifiesDirectionsBeforeChoosing pins step 2 as the place
// where directions are weighed and proven feasible against the code, not a
// quiz on what the user knows. A direction chosen unverified sends every later
// section into a design that cannot be built.
func TestCoDesignPrompt_VerifiesDirectionsBeforeChoosing(t *testing.T) {
	t.Parallel()

	tpl := normalizedCoDesignTemplate()

	for name, clause := range map[string]string{
		"feasibility defined":           "A direction is feasible when it fits the code, meets every challenge and hard constraint from step 1, and handles the problem cases in the Background.",
		"trace a problem case":          "trace a problem case through the code as the direction would change it",
		"user direction verified first": "**The user brings a direction:** verify it.",
		"failed direction shown":        "show the failing step and the challenge or constraint it breaks",
		"propose only feasible":         "Propose only directions you verified",
		"unsolvable said plainly":       "**Nothing is feasible:** tell the user plainly the problem cannot be solved under its constraints",
		"no silent relaxing":            "Do not relax a constraint yourself or present a failing direction as working.",
	} {
		require.Contains(t, tpl, clause, name)
	}
	require.NotContains(t, tpl, "I don't know")
}

// TestCoDesignPrompt_MechanismsRefineTheDirection pins step 3 as a refinement
// of the direction step 2 verified, not a fresh design. Sections cut free of
// the thesis can drift into a solution nobody checked for feasibility.
func TestCoDesignPrompt_MechanismsRefineTheDirection(t *testing.T) {
	t.Parallel()

	tpl := normalizedCoDesignTemplate()

	for name, clause := range map[string]string{
		"refine, no new idea":         "Refine the direction settled in step 2; bring in no new idea.",
		"outline from the thesis":     "Derive the outline from the thesis",
		"boundaries from the trace":   "the points where the trace passes from one mechanism to another",
		"section keeps the outcome":   "it must reach the outcome step 2's trace reached",
		"departure reopens direction": "take the failing case back to step 2; never reshape the direction inside a section",
	} {
		require.Contains(t, tpl, clause, name)
	}
}

// TestCoDesignPrompt_MergesWhenFeasibleSolvedAndKnown pins the merge bar to
// what the document has to guarantee its readers: the design can be built,
// it meets the problem the user stated, and the user has seen every piece of
// it. A bar built from bookkeeping, such as every item ticked off, lets a
// design that solves nothing pass.
func TestCoDesignPrompt_MergesWhenFeasibleSolvedAndKnown(t *testing.T) {
	t.Parallel()

	tpl := normalizedCoDesignTemplate()

	for name, clause := range map[string]string{
		"feasible":                 "1. **The solution is feasible.**",
		"solves the problem":       "2. **It solves the problem.** Every challenge is met by a mechanism",
		"user knows the design":    "3. **The user knows every piece of the design.**",
		"step 4 checks challenges": "For each challenge, name the mechanism that meets it and how.",
	} {
		require.Contains(t, tpl, clause, name)
	}
}

// TestCoDesignPrompt_HandsBackASolutionNotAPlan pins co-design's role as the
// designer of a whole solution that the user exports and splits into plans.
// A template that drifts back to an implementation plan would have the coder
// start building the whole solution the moment the branch merges.
func TestCoDesignPrompt_HandsBackASolutionNotAPlan(t *testing.T) {
	t.Parallel()

	tpl := normalizedCoDesignTemplate()

	for _, clause := range []string{
		"1. **Background**",
		"2. **Design**",
		"3. **Trade-offs**",
		"4. **Risks and preconditions**",
		"5. **Follow-up work**",
		"This solution is built through plans, one part at a time.",
		"Derive the outline",
		"A suggestion or open question is settled only by an answer that names it",
	} {
		require.Contains(t, tpl, clause)
	}
	require.NotContains(t, tpl, "Implement the steps above")
	require.NotContains(t, tpl, "Plan breakdown")
}

// TestCoDesignPrompt_LeavesDetailsToThePlans pins the line between co-design
// and the plans that follow it. Under the contract, everything written into
// the document binds those plans, so a template that records function bodies,
// constants or line-level change lists takes those choices away from the
// plans that have to make them.
func TestCoDesignPrompt_LeavesDetailsToThePlans(t *testing.T) {
	t.Parallel()

	tpl := normalizedCoDesignTemplate()

	for name, clause := range map[string]string{
		"document stands on its own":         "Readers who never saw this conversation must be able to build each part from it without reopening its design.",
		"details stay out of co-design":      "never ask about it or write it down.",
		"no function bodies":                 "Never write a function body.",
		"section ends where building can":    "A section is complete when a reader who was not here can work out how to build it, and why it is correct, from the section alone",
		"document does not narrate the flow": "Outside the contract, never mention plans or what the document leaves out.",
		"contract hands omissions over":      "What the design leaves unsaid, such as code layout, function boundaries, private names, and tests, is the plan's to decide.",
	} {
		require.Contains(t, tpl, clause, name)
	}
	require.NotContains(t, tpl, "// detail:")
}

// TestCommitPrompt_Builds pins that the commit agent's built-in
// template resolves and renders like any other internal-agent prompt,
// the same way compactPrompt and webFetchPrompt are exercised only
// through their own constructors elsewhere.
func TestCommitPrompt_Builds(t *testing.T) {
	dir := t.TempDir()
	globalDir := t.TempDir()
	t.Setenv("ANGELA_GLOBAL_CONFIG", globalDir)
	t.Setenv("ANGELA_GLOBAL_DATA", globalDir)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "angela.json"),
		[]byte(`{
			"options": {"disable_default_providers": true},
			"providers": {"test": {"base_url": "http://127.0.0.1:0/v1", "api_key": "test",
				"models": [{"id": "test-model", "name": "Test"}]}}
		}`), 0o644))

	store, err := config.Init(dir, "", false)
	require.NoError(t, err)

	p, err := commitPrompt()
	require.NoError(t, err)

	out, err := p.Build(context.Background(), "", "", store)
	require.NoError(t, err)
	require.NotEmpty(t, out)
}
