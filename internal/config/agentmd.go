package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/NaturalSelect/angela/internal/home"
	"gopkg.in/yaml.v3"
)

// AgentFrontmatter mirrors the Agent fields that can appear in YAML
// frontmatter of a markdown agent file. It is also the shape
// RenderAgentFile writes, so parsing and rendering share one
// definition of what a markdown agent file looks like.
type AgentFrontmatter struct {
	Name          string          `yaml:"name,omitempty"`
	Description   string          `yaml:"description,omitempty"`
	Mode          string          `yaml:"mode,omitempty"`
	Model         string          `yaml:"model,omitempty"`
	Variant       string          `yaml:"variant,omitempty"`
	Temperature   *float64        `yaml:"temperature,omitempty"`
	AllowedTools  *AllowedToolSet `yaml:"allowed_tools,omitempty"`
	DisabledTools []string        `yaml:"disabled_tools,omitempty"`
	AllowedMCP    *AllowedMCPSet  `yaml:"allowed_mcp,omitempty"`
	Disabled      *bool           `yaml:"disabled,omitempty"`
	Hidden        *bool           `yaml:"hidden,omitempty"`
	MaxTokens     *int64          `yaml:"max_tokens,omitempty"`
}

// agentIDPattern is a strict allowlist: lowercase alphanumeric
// segments separated by single hyphens. Since '/', '\', '.', and
// whitespace are never in the allowed character set, a matching id
// can never traverse a path or collide with a hidden file.
var agentIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidateAgentID reports whether id is safe to use as an agent
// identifier and, by extension, as a markdown filename stem. It is
// shared by markdown file discovery (the filename stem must match)
// and the agent generation path, where an LLM-produced identifier
// must not be trusted to stay inside the generated agents directory.
func ValidateAgentID(id string) error {
	if !agentIDPattern.MatchString(id) {
		return fmt.Errorf("invalid agent id %q: must be lowercase alphanumeric segments separated by single hyphens", id)
	}
	return nil
}

// GlobalAgentDirs returns the default global directories for agent
// markdown files, in priority-ascending order: a later directory's
// file overrides an earlier directory's file with the same ID.
func GlobalAgentDirs() []string {
	if envDir := os.Getenv("ANGELA_AGENTS_DIR"); envDir != "" {
		return []string{envDir}
	}

	paths := []string{
		filepath.Join(home.Dir(), ".claude", "agents"),
		filepath.Join(home.Dir(), ".agents", "agents"),
		filepath.Join(home.Config(), appName, "agents"),
	}

	if runtime.GOOS == "windows" {
		appData := os.Getenv("LOCALAPPDATA")
		if appData == "" {
			appData = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		paths = append(
			paths,
			filepath.Join(appData, "agents", "agents"),
			filepath.Join(appData, appName, "agents"),
		)
	}

	return paths
}

// generatedAgentSubdir is the project-relative directory where "angela
// agent create" writes generated agent files. It is also the
// highest-priority entry in projectAgentSubdirs.
const generatedAgentSubdir = ".angela/agents"

// projectAgentSubdirs lists the conventional subdirectories where
// project-level agent markdown files are discovered, in
// priority-ascending order: a later entry overrides an earlier one on
// ID conflict.
var projectAgentSubdirs = []string{
	".claude/agents",
	".agents/agents",
	generatedAgentSubdir,
}

// GeneratedAgentDir returns the directory where generated agent
// markdown files are written for workingDir.
func GeneratedAgentDir(workingDir string) string {
	return filepath.Join(workingDir, generatedAgentSubdir)
}

// ProjectAgentDirs returns the default project directories for agent
// markdown files, including both the git worktree root and the
// working directory. Root paths come first (lower priority);
// working-directory paths come last so a local agent overrides a
// monorepo-level one sharing the same ID.
func ProjectAgentDirs(workingDir string) []string {
	dirs := make([]string, 0, len(projectAgentSubdirs)*2)

	if root := worktreeRoot(workingDir); root != "" && root != workingDir {
		for _, sub := range projectAgentSubdirs {
			dirs = append(dirs, filepath.Join(root, sub))
		}
	}

	for _, sub := range projectAgentSubdirs {
		dirs = append(dirs, filepath.Join(workingDir, sub))
	}

	return dirs
}

// DiscoverAgentFiles scans the given directories for *.md files and
// parses them as agent definitions. Each file's name (without
// extension) becomes the agent ID. Directories are scanned in the
// given order, which is priority-ascending: a later directory's file
// overrides an earlier directory's file with the same ID. Parse
// errors are logged and skipped.
//
// Symlinked directories and files are skipped. An agent file defines a
// system prompt and a tool whitelist, so following a link would let
// anything with write access to a scanned directory point at a file
// outside it and hand that file's contents to the model as
// instructions.
func DiscoverAgentFiles(paths []string) map[string]Agent {
	agents := make(map[string]Agent)

	for _, dir := range paths {
		if info, err := os.Lstat(dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
			slog.Warn("Skipping symlinked agent directory", "dir", dir)
			continue
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			// Directory not existing is normal; only warn on
			// unexpected errors.
			if !os.IsNotExist(err) {
				slog.Warn("Failed to read agent directory", "dir", dir, "error", err)
			}
			continue
		}

		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}

			id := strings.TrimSuffix(entry.Name(), ".md")
			path := filepath.Join(dir, entry.Name())

			// DirEntry.Type reports the lstat mode, so this needs no
			// extra syscall.
			if entry.Type()&os.ModeSymlink != 0 {
				slog.Warn("Skipping symlinked agent file", "path", path)
				continue
			}

			if err := ValidateAgentID(id); err != nil {
				slog.Warn("Skipping agent file with invalid id", "path", path, "error", err)
				continue
			}

			agent, err := parseAgentFile(path)
			if err != nil {
				slog.Warn("Failed to parse agent file", "path", path, "error", err)
				continue
			}

			agent.ID = id
			agents[id] = agent
		}
	}

	return agents
}

// parseAgentFile reads a markdown file with optional YAML frontmatter
// and returns an Agent. The frontmatter provides structured fields;
// the body becomes the Prompt.
func parseAgentFile(path string) (Agent, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return Agent{}, err
	}
	return ParseAgentContent(string(content))
}

// ParseAgentContent parses markdown agent file content (with optional
// YAML frontmatter) into an Agent. The frontmatter provides
// structured fields; the body becomes the Prompt. Besides markdown
// file discovery, the agent generation path uses this to
// self-validate a generated file's content before writing it to
// disk.
func ParseAgentContent(content string) (Agent, error) {
	frontmatter, body, hasFrontmatter, err := splitAgentFrontmatter(content)
	if err != nil {
		return Agent{}, err
	}

	prompt := strings.TrimSpace(body)
	if !hasFrontmatter && prompt == "" {
		return Agent{}, fmt.Errorf("empty agent file")
	}

	var agent Agent
	if hasFrontmatter {
		var fm AgentFrontmatter
		// KnownFields rejects unrecognized keys instead of dropping
		// them: a typo in a permission field (allowed_tool for
		// allowed_tools) must not silently fall back to the broader
		// default.
		dec := yaml.NewDecoder(strings.NewReader(frontmatter))
		dec.KnownFields(true)
		if err := dec.Decode(&fm); err != nil && !errors.Is(err, io.EOF) {
			return Agent{}, err
		}
		agent.Name = fm.Name
		agent.Description = fm.Description
		agent.Mode = AgentMode(fm.Mode)
		agent.Model = ModelConfigName(fm.Model)
		agent.Variant = fm.Variant
		agent.Temperature = fm.Temperature
		agent.AllowedTools = fm.AllowedTools
		agent.DisabledTools = fm.DisabledTools
		agent.AllowedMCP = fm.AllowedMCP
		agent.Disabled = fm.Disabled
		agent.Hidden = fm.Hidden
		agent.MaxTokens = fm.MaxTokens
		if err := validateAgentFields(agent); err != nil {
			return Agent{}, err
		}
	}

	if prompt != "" {
		agent.Prompt = prompt
	}

	return agent, nil
}

// RenderAgentFile serializes fm as YAML frontmatter followed by body,
// producing markdown agent file content in the same shape
// ParseAgentContent reads. The agent generation path uses this
// instead of string interpolation so a description or system prompt
// containing YAML-significant characters (e.g. a colon) can't
// corrupt the frontmatter.
func RenderAgentFile(fm AgentFrontmatter, body string) ([]byte, error) {
	yamlBytes, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("failed to render agent frontmatter: %w", err)
	}
	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(yamlBytes)
	buf.WriteString("---\n\n")
	buf.WriteString(strings.TrimSpace(body))
	buf.WriteString("\n")
	return buf.Bytes(), nil
}

// ValidateAgent checks an agent definition coming from any config
// layer. Markdown files are validated while being parsed; JSON and
// angelarc definitions go through ResolveAgents, which calls this so
// every layer rejects the same values instead of only the markdown
// one.
func ValidateAgent(id string, a Agent) error {
	if err := ValidateAgentID(id); err != nil {
		return err
	}
	return validateAgentFields(a)
}

// validateAgentFields checks the enum and range constraints on an
// agent, independent of its ID. ParseAgentContent uses it directly
// because a markdown file's ID comes from its filename, which
// DiscoverAgentFiles has already validated.
func validateAgentFields(a Agent) error {
	switch a.Mode {
	case "", AgentModePrimary, AgentModeSubagent:
	default:
		return fmt.Errorf("invalid mode %q: must be one of %s, %s", a.Mode, AgentModePrimary, AgentModeSubagent)
	}
	// Agent.Model is an open value domain: any model config name is
	// accepted here, and an unknown one is warned about and falls back
	// to ModelMain at resolution time.
	if a.Temperature != nil {
		t := *a.Temperature
		// NaN fails both comparisons of a plain range check, so it
		// has to be rejected explicitly.
		if math.IsNaN(t) || math.IsInf(t, 0) || t < 0 || t > 1 {
			return fmt.Errorf("invalid temperature %v: must be between 0 and 1", t)
		}
	}
	return nil
}

// splitAgentFrontmatter extracts YAML frontmatter and body from
// markdown content. Returns empty frontmatter and the full content if
// no frontmatter delimiters are found, with hasFrontmatter=false. An
// opening "---" with no matching closing delimiter is an error rather
// than being silently treated as body text.
func splitAgentFrontmatter(content string) (frontmatter, body string, hasFrontmatter bool, err error) {
	content = strings.TrimPrefix(content, "\uFEFF")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	lines := strings.Split(content, "\n")

	// Find start of frontmatter.
	start := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if trimmed == "---" {
			start = i
		}
		break
	}
	if start == -1 {
		return "", content, false, nil
	}

	// Find end of frontmatter.
	for i := start + 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			fm := strings.Join(lines[start+1:i], "\n")
			bd := strings.Join(lines[i+1:], "\n")
			return fm, bd, true, nil
		}
	}

	return "", "", false, fmt.Errorf("unterminated frontmatter")
}
