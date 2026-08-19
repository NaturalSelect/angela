package agent

import (
	"context"
	_ "embed"

	"github.com/NaturalSelect/angela/internal/agent/prompt"
	"github.com/NaturalSelect/angela/internal/config"
)

//go:embed templates/coder.md.tpl
var coderPromptTmpl []byte

//go:embed templates/task.md.tpl
var taskPromptTmpl []byte

//go:embed templates/explore.md.tpl
var explorePromptTmpl []byte

//go:embed templates/general.md.tpl
var generalPromptTmpl []byte

//go:embed templates/initialize.md.tpl
var initializePromptTmpl []byte

func coderPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	systemPrompt, err := prompt.NewPrompt("coder", string(coderPromptTmpl), opts...)
	if err != nil {
		return nil, err
	}
	return systemPrompt, nil
}

func taskPrompt(opts ...prompt.Option) (*prompt.Prompt, error) {
	systemPrompt, err := prompt.NewPrompt("task", string(taskPromptTmpl), opts...)
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

// builtinPromptForAgent returns the built-in prompt for a known agent
// ID. Returns nil for unknown IDs — the caller falls back to the
// general template.
var builtinPromptForAgent = map[string]func(...prompt.Option) (*prompt.Prompt, error){
	config.AgentCoder:   coderPrompt,
	config.AgentTask:    taskPrompt,
	config.AgentExplore: explorePrompt,
	config.AgentGeneral: generalPrompt,
}

// agentPrompt resolves the system prompt for an agent. If the agent
// config has a custom Prompt string it is used as a template;
// otherwise the built-in template for the agent ID is used, falling
// back to the general template for unknown IDs. The agent's own
// ContextPaths (set by three-layer config resolution) always take
// precedence over the global default, on every path.
func agentPrompt(agentCfg config.Agent, opts ...prompt.Option) (*prompt.Prompt, error) {
	opts = append(opts, prompt.WithContextPaths(agentCfg.ContextPaths))

	if agentCfg.Prompt != "" {
		return prompt.NewPrompt(agentCfg.ID, agentCfg.Prompt, opts...)
	}

	if fn, ok := builtinPromptForAgent[agentCfg.ID]; ok {
		return fn(opts...)
	}

	// Unknown agent ID — use the general template.
	return generalPrompt(opts...)
}

func InitializePrompt(cfg *config.ConfigStore) (string, error) {
	systemPrompt, err := prompt.NewPrompt("initialize", string(initializePromptTmpl))
	if err != nil {
		return "", err
	}
	return systemPrompt.Build(context.Background(), "", "", cfg)
}
