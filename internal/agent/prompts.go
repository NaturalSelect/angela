package agent

import (
	"context"
	_ "embed"

	"github.com/NaturalSelect/angela/internal/agent/prompt"
	"github.com/NaturalSelect/angela/internal/config"
)

//go:embed templates/coder.md.tpl
var coderPromptTmpl []byte

//go:embed templates/explore.md.tpl
var explorePromptTmpl []byte

//go:embed templates/general.md.tpl
var generalPromptTmpl []byte

//go:embed templates/plan.md.tpl
var planPromptTmpl []byte

//go:embed templates/deep_research.md.tpl
var deepResearchPromptTmpl []byte

//go:embed templates/initialize.md.tpl
var initializePromptTmpl []byte

//go:embed templates/title.md
var titlePromptTmpl []byte

//go:embed templates/summary.md
var summaryPromptTmpl []byte

//go:embed templates/web_fetch_prompt.md.tpl
var webFetchPromptTmpl []byte

//go:embed templates/generate_agent.md.tpl
var generateAgentPromptTmpl []byte

// branchPreambleTmpl fronts the system prompt of every branch-mode agent,
// the built-in plan and deep-research agents as well as any the user
// defines. It is the only place the rules the fork machinery depends on are
// guaranteed to be stated, so a custom branch prompt cannot drop them by
// omission.
//
//go:embed templates/branch_preamble.md.tpl
var branchPreambleTmpl []byte

func coderPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	systemPrompt, err := prompt.NewPrompt("coder", string(coderPromptTmpl), opts...)
	if err != nil {
		return nil, err
	}
	return systemPrompt, nil
}

func explorePrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	systemPrompt, err := prompt.NewPrompt("explore", string(explorePromptTmpl), opts...)
	if err != nil {
		return nil, err
	}
	return systemPrompt, nil
}

func generalPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	systemPrompt, err := prompt.NewPrompt("general", string(generalPromptTmpl), opts...)
	if err != nil {
		return nil, err
	}
	return systemPrompt, nil
}

func planPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	return prompt.NewPrompt(config.AgentPlan, string(planPromptTmpl), opts...)
}

func deepResearchPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	return prompt.NewPrompt(config.AgentDeepResearch, string(deepResearchPromptTmpl), opts...)
}

func titlePrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	return prompt.NewPrompt(config.AgentTitle, string(titlePromptTmpl), opts...)
}

func compactPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	return prompt.NewPrompt(config.AgentCompact, string(summaryPromptTmpl), opts...)
}

func webFetchPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	return prompt.NewPrompt(config.AgentWebFetch, string(webFetchPromptTmpl), opts...)
}

func generateAgentPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	return prompt.NewPrompt(config.AgentGenerateAgent, string(generateAgentPromptTmpl), opts...)
}

func initializePrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	return prompt.NewPrompt(config.AgentInitialize, string(initializePromptTmpl), opts...)
}

// builtinPromptForAgent returns the built-in prompt for a known agent
// ID. Returns nil for unknown IDs — the caller falls back to the
// general template.
var builtinPromptForAgent = map[string]func(...prompt.Option) (*prompt.Prompt, error){
	config.AgentCoder:         coderPrompt,
	config.AgentDeepResearch:  deepResearchPrompt,
	config.AgentExplore:       explorePrompt,
	config.AgentGeneral:       generalPrompt,
	config.AgentPlan:          planPrompt,
	config.AgentTitle:         titlePrompt,
	config.AgentCompact:       compactPrompt,
	config.AgentWebFetch:      webFetchPrompt,
	config.AgentGenerateAgent: generateAgentPrompt,
	config.AgentInitialize:    initializePrompt,
}

// builtinPromptTemplateFile names the template each built-in prompt is
// embedded from. The map above holds closures, which reflection cannot
// see through, so this parallel table is what lets a test prove every
// template under templates/ is actually reachable as an agent prompt.
// Adding a template without registering it here fails that test.
var builtinPromptTemplateFile = map[string]string{
	config.AgentCoder:         "coder.md.tpl",
	config.AgentDeepResearch:  "deep_research.md.tpl",
	config.AgentExplore:       "explore.md.tpl",
	config.AgentGeneral:       "general.md.tpl",
	config.AgentPlan:          "plan.md.tpl",
	config.AgentTitle:         "title.md",
	config.AgentCompact:       "summary.md",
	config.AgentWebFetch:      "web_fetch_prompt.md.tpl",
	config.AgentGenerateAgent: "generate_agent.md.tpl",
	config.AgentInitialize:    "initialize.md.tpl",
}

// agentPrompt resolves the system prompt for an agent. If the agent
// config has a custom Prompt string it is used as a template;
// otherwise the built-in template for the agent ID is used, falling
// back to the general template for unknown IDs. The agent's own
// ContextPaths (set by three-layer config resolution) always take
// precedence over the global default, on every path.
func agentPrompt(agentCfg config.Agent, opts ...prompt.Option) (*prompt.Prompt, error) {
	opts = append(opts, prompt.WithContextPaths(agentCfg.ContextPaths))

	// Applied here rather than inside any one template so that all three
	// resolutions below carry it, including a custom prompt that replaces
	// the template outright.
	if agentCfg.Mode == config.AgentModeBranch {
		opts = append(opts, prompt.WithPreamble(string(branchPreambleTmpl)))
	}

	if agentCfg.Prompt != "" {
		return prompt.NewPrompt(agentCfg.ID, agentCfg.Prompt, opts...)
	}

	if fn, ok := builtinPromptForAgent[agentCfg.ID]; ok {
		return fn(opts...)
	}

	// Unknown agent ID — use the general template.
	return generalPrompt(opts...)
}

// InitializePrompt renders the user message that seeds project
// initialization. Unlike the other internal agents, initialize never
// makes an LLM call of its own — the rendered text is injected into an
// ordinary session and runs on whatever agent is primary at the time.
// Its prompt is the only thing it owns, and going through agentPrompt
// is what makes that prompt overridable through the normal three-layer
// config path.
func InitializePrompt(store *config.ConfigStore) (string, error) {
	agentCfg, ok := store.Config().Agents[config.AgentInitialize]
	if !ok {
		agentCfg = config.Agent{ID: config.AgentInitialize}
	}
	systemPrompt, err := agentPrompt(agentCfg)
	if err != nil {
		return "", err
	}
	return systemPrompt.Build(context.Background(), "", "", store)
}
