package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"charm.land/fantasy"
	"charm.land/fantasy/schema"

	"github.com/NaturalSelect/angela/internal/agent/prompt"
	"github.com/NaturalSelect/angela/internal/config"
)

// GeneratedAgent is the structured output schema for LLM-based agent
// generation.
type GeneratedAgent struct {
	Identifier   string `json:"identifier" description:"A unique descriptive identifier using lowercase letters, numbers, and hyphens"`
	WhenToUse    string `json:"when_to_use" description:"A description starting with Use when... that defines triggering conditions"`
	SystemPrompt string `json:"system_prompt" description:"The complete system prompt governing the agent behavior"`
}

// GenerateAgent uses the LLM to generate an agent definition from a
// natural language description. It writes the result as a markdown
// file and returns the agent config and file path.
func (c *coordinator) GenerateAgent(ctx context.Context, description string) (config.Agent, string, error) {
	// Collect existing agent IDs to prevent collisions.
	existingIDs := make([]string, 0, len(c.cfg.Config().Agents))
	for id := range c.cfg.Config().Agents {
		existingIDs = append(existingIDs, id)
	}
	sort.Strings(existingIDs)

	_, model, systemPrompt, err := c.resolveInternalAgent(ctx, config.AgentGenerateAgent,
		prompt.WithWorkingDir(c.cfg.WorkingDir()),
		prompt.WithExtra(map[string]any{"ExistingIDs": strings.Join(existingIDs, ", ")}),
	)
	if err != nil {
		return config.Agent{}, "", fmt.Errorf("resolve the generate-agent agent: %w", err)
	}

	// Call the LLM for structured output.
	resp, err := model.Model.GenerateObject(ctx, fantasy.ObjectCall{
		Prompt: fantasy.Prompt{
			fantasy.NewSystemMessage(systemPrompt),
			fantasy.NewUserMessage(fmt.Sprintf("Create an agent configuration based on this request: %q", description)),
		},
		Schema:     schema.Generate(reflect.TypeOf(GeneratedAgent{})),
		SchemaName: "agent",
	})
	if err != nil {
		return config.Agent{}, "", fmt.Errorf("generate object: %w", err)
	}

	// Unmarshal the response object.
	objBytes, err := json.Marshal(resp.Object)
	if err != nil {
		return config.Agent{}, "", fmt.Errorf("marshal response: %w", err)
	}
	var generated GeneratedAgent
	if err := json.Unmarshal(objBytes, &generated); err != nil {
		return config.Agent{}, "", fmt.Errorf("unmarshal response: %w", err)
	}

	if generated.Identifier == "" {
		return config.Agent{}, "", fmt.Errorf("LLM returned empty identifier")
	}

	return c.writeGeneratedAgent(generated)
}

// writeGeneratedAgent validates and writes a GeneratedAgent to disk. It
// is split out from GenerateAgent so this validation and file-handling
// logic can be exercised directly in tests without mocking the LLM.
func (c *coordinator) writeGeneratedAgent(generated GeneratedAgent) (config.Agent, string, error) {
	generated.Identifier = strings.TrimSpace(generated.Identifier)
	generated.WhenToUse = strings.TrimSpace(generated.WhenToUse)
	generated.SystemPrompt = strings.TrimSpace(generated.SystemPrompt)

	if err := config.ValidateAgentID(generated.Identifier); err != nil {
		return config.Agent{}, "", err
	}
	if generated.WhenToUse == "" {
		return config.Agent{}, "", errors.New("LLM returned an empty description")
	}
	if generated.SystemPrompt == "" {
		return config.Agent{}, "", errors.New("LLM returned an empty system prompt")
	}
	if _, exists := c.cfg.Config().Agents[generated.Identifier]; exists {
		return config.Agent{}, "", fmt.Errorf("agent %q already exists", generated.Identifier)
	}

	agentDir := config.GeneratedAgentDir(c.cfg.WorkingDir())
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return config.Agent{}, "", fmt.Errorf("create agent directory: %w", err)
	}
	if err := requireInsideWorkspace(agentDir, c.cfg.WorkingDir()); err != nil {
		return config.Agent{}, "", err
	}

	path := filepath.Join(agentDir, generated.Identifier+".md")

	content, err := config.RenderAgentFile(config.AgentFrontmatter{
		Description: generated.WhenToUse,
		Mode:        string(config.AgentModeSubagent),
	}, generated.SystemPrompt)
	if err != nil {
		return config.Agent{}, "", fmt.Errorf("render agent file: %w", err)
	}

	// Self-validate before writing: a file GenerateAgent can't parse
	// back itself must never reach disk.
	parsed, err := config.ParseAgentContent(string(content))
	if err != nil {
		return config.Agent{}, "", fmt.Errorf("generated agent file failed self-validation: %w", err)
	}
	parsed.ID = generated.Identifier

	// A prompt that only fails when it is executed would leave a
	// permanently broken agent on disk, so run it through the same
	// loader a dispatch would.
	if _, err := agentPrompt(parsed, prompt.WithWorkingDir(c.cfg.WorkingDir())); err != nil {
		return config.Agent{}, "", fmt.Errorf("generated system prompt is not a valid template: %w", err)
	}

	if err := writeNewFileAtomic(path, content); err != nil {
		return config.Agent{}, "", err
	}

	return parsed, path, nil
}

// requireInsideWorkspace fails unless dir resolves to a location inside
// the workspace. Both sides are resolved through symlinks first, so a
// linked agent directory cannot be used to write generated prompts
// somewhere the user never agreed to.
func requireInsideWorkspace(dir, workingDir string) error {
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return fmt.Errorf("resolve agent directory: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	rel, err := filepath.Rel(realRoot, realDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("agent directory %q is outside the workspace", dir)
	}
	return nil
}

// writeNewFileAtomic writes content to path via a temporary file in the
// same directory, so a crash or a short write cannot leave a partially
// written agent behind. os.Link supplies the "must not already exist"
// guarantee that a plain rename would silently overwrite.
func writeNewFileAtomic(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agent-*.md")
	if err != nil {
		return fmt.Errorf("create temp agent file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := writeAndSync(tmp, content); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("chmod agent file: %w", err)
	}
	if err := os.Link(tmpName, path); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("agent file already exists: %s", path)
		}
		return fmt.Errorf("publish agent file: %w", err)
	}
	return nil
}

// writeAndSync writes content and closes f, reporting the error from
// either step. Close is what surfaces a failed flush on many
// filesystems, so ignoring it can turn a failed write into a silent
// truncation.
func writeAndSync(f *os.File, content []byte) error {
	if _, err := f.Write(content); err != nil {
		f.Close()
		return fmt.Errorf("write agent file: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync agent file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close agent file: %w", err)
	}
	return nil
}
