package config

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NaturalSelect/angela/internal/home"
	"github.com/stretchr/testify/require"
)

func TestSetDefaults_AgentPathsExpansionAndIdempotency(t *testing.T) {
	dollarHome := t.TempDir()
	t.Setenv("HOME", dollarHome)
	workingDir := t.TempDir()

	cfg := &Config{Options: &Options{AgentPaths: []string{"~/x", "$HOME/y", "./z"}}}
	cfg.setDefaults(workingDir, t.TempDir())

	first := cfg.Options.AgentPaths
	for _, p := range first {
		require.True(t, filepath.IsAbs(p), "path %q should be absolute", p)
	}
	require.Contains(t, first, filepath.Join(workingDir, "z"))
	require.Contains(t, first, filepath.Join(home.Dir(), "x"))
	require.Contains(t, first, filepath.Join(dollarHome, "y"))

	cfg.setDefaults(workingDir, t.TempDir())
	require.Equal(t, first, cfg.Options.AgentPaths, "a second setDefaults call must reproduce the same list")
}

func TestDiscoverAgentFiles_ProjectOverridesGlobal(t *testing.T) {
	globalDir := t.TempDir()
	t.Setenv("ANGELA_AGENTS_DIR", globalDir)
	require.NoError(t, os.WriteFile(filepath.Join(globalDir, "foo.md"), []byte("global body"), 0o644))

	workingDir := t.TempDir()
	projectDir := GeneratedAgentDir(workingDir)
	require.NoError(t, os.MkdirAll(projectDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projectDir, "foo.md"), []byte("project body"), 0o644))

	cfg := &Config{Options: &Options{}}
	cfg.setDefaults(workingDir, t.TempDir())

	agents := DiscoverAgentFiles(cfg.Options.AgentPaths)
	require.Contains(t, agents, "foo")
	require.Equal(t, "project body", agents["foo"].Prompt)
}

// TestDiscoverAgentFiles_SkipsSymlinkedFile stops a link inside a
// scanned directory from pulling in a file the user never placed there.
// An agent file is a system prompt plus a tool whitelist, so following
// the link would feed arbitrary content to the model as instructions.
func TestDiscoverAgentFiles_SkipsSymlinkedFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevated privileges on Windows")
	}

	outside := t.TempDir()
	target := filepath.Join(outside, "evil.md")
	require.NoError(t, os.WriteFile(target, []byte("---\ndescription: evil\n---\nevil body"), 0o644))

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "good.md"),
		[]byte("---\ndescription: good\n---\ngood body"), 0o644))
	require.NoError(t, os.Symlink(target, filepath.Join(dir, "evil.md")))

	agents := DiscoverAgentFiles([]string{dir})
	require.Contains(t, agents, "good", "a regular agent file must still load")
	require.NotContains(t, agents, "evil", "a symlinked agent file must be skipped")
}

func TestDiscoverAgentFiles_SkipsSymlinkedDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevated privileges on Windows")
	}

	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "evil.md"),
		[]byte("---\ndescription: evil\n---\nevil body"), 0o644))

	link := filepath.Join(t.TempDir(), "agents")
	require.NoError(t, os.Symlink(outside, link))

	require.Empty(t, DiscoverAgentFiles([]string{link}),
		"a symlinked agent directory must be skipped entirely")
}

func TestSplitAgentFrontmatter(t *testing.T) {
	tests := []struct {
		name            string
		content         string
		wantFrontmatter string
		wantBody        string
		wantHas         bool
		wantErr         bool
	}{
		{
			name:     "no frontmatter",
			content:  "You are a helpful agent.",
			wantBody: "You are a helpful agent.",
		},
		{
			name:    "empty file",
			content: "",
		},
		{
			name:            "BOM prefixed",
			content:         "\uFEFF---\ndescription: x\n---\nbody text",
			wantFrontmatter: "description: x",
			wantBody:        "body text",
			wantHas:         true,
		},
		{
			name:            "CRLF line endings",
			content:         "---\r\ndescription: x\r\n---\r\nbody text",
			wantFrontmatter: "description: x",
			wantBody:        "body text",
			wantHas:         true,
		},
		{
			name:            "leading blank lines before delimiter",
			content:         "\n\n---\ndescription: x\n---\nbody",
			wantFrontmatter: "description: x",
			wantBody:        "body",
			wantHas:         true,
		},
		{
			name:    "unterminated frontmatter",
			content: "---\ndescription: x\nno closing delimiter",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fm, body, has, err := splitAgentFrontmatter(tt.content)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantHas, has)
			require.Equal(t, tt.wantFrontmatter, fm)
			require.Equal(t, tt.wantBody, body)
		})
	}
}

func TestParseAgentFile_Validation(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{
			name:    "invalid mode",
			content: "---\nmode: primray\n---\nbody",
			wantErr: "invalid mode",
		},
		{
			name:    "invalid model",
			content: "---\nmodel: smal\n---\nbody",
			wantErr: "invalid model",
		},
		{
			name:    "temperature out of range",
			content: "---\ntemperature: 1.5\n---\nbody",
			wantErr: "invalid temperature",
		},
		{
			name: "fully valid",
			content: "---\n" +
				"name: Reviewer\n" +
				"description: Reviews code\n" +
				"mode: subagent\n" +
				"model: small\n" +
				"temperature: 0.2\n" +
				"allowed_tools: [view, grep]\n" +
				"disabled_tools: [bash]\n" +
				"disabled: false\n" +
				"---\n" +
				"You are a reviewer.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "agent.md")
			require.NoError(t, os.WriteFile(path, []byte(tt.content), 0o644))

			agent, err := parseAgentFile(path)
			if tt.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.name != "fully valid" {
				return
			}
			require.Equal(t, "Reviewer", agent.Name)
			require.Equal(t, "Reviews code", agent.Description)
			require.Equal(t, AgentModeSubagent, agent.Mode)
			require.Equal(t, SelectedModelTypeSmall, agent.Model)
			require.NotNil(t, agent.Temperature)
			require.InDelta(t, 0.2, *agent.Temperature, 0.0001)
			require.Equal(t, &AllowedToolSet{Kind: ToolSetScope, Tools: []string{"view", "grep"}}, agent.AllowedTools)
			require.Equal(t, []string{"bash"}, agent.DisabledTools)
			require.NotNil(t, agent.Disabled)
			require.False(t, *agent.Disabled)
			require.Equal(t, "You are a reviewer.", agent.Prompt)
		})
	}
}

func TestDiscoverAgentFiles_SkipsInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.md"), []byte("---\nmode: bogus\nno closing"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "good.md"), []byte("Just a prompt body."), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Invalid_Name.md"), []byte("Body."), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "empty.md"), []byte("   \n\n"), 0o644))

	agents := DiscoverAgentFiles([]string{dir})
	require.NotContains(t, agents, "bad")
	require.NotContains(t, agents, "Invalid_Name", "a filename stem that fails ValidateAgentID must be skipped")
	require.NotContains(t, agents, "empty", "a file with no frontmatter and only whitespace body must be skipped")
	require.Contains(t, agents, "good")
}

func TestValidateAgentID(t *testing.T) {
	valid := []string{"a", "foo", "a1", "a-b", "a-b-1", "reviewer", "code-reviewer-2"}
	for _, id := range valid {
		t.Run("valid/"+id, func(t *testing.T) {
			require.NoError(t, ValidateAgentID(id))
		})
	}

	invalid := []string{"", "../evil", "a/b", "a\\b", "Foo", "a_b", "-a", "a-", "a--b", "a.b", "/etc/passwd", "foo "}
	for _, id := range invalid {
		t.Run("invalid/"+id, func(t *testing.T) {
			require.Error(t, ValidateAgentID(id))
		})
	}
}

func TestRenderAgentFile_RoundTrip(t *testing.T) {
	temp := 0.3
	disabled := false
	fm := AgentFrontmatter{
		Name:          "Reviewer",
		Description:   "Use when: reviewing API changes",
		Mode:          "subagent",
		Model:         "small",
		Temperature:   &temp,
		AllowedTools:  &AllowedToolSet{Kind: ToolSetScope, Tools: []string{"view", "grep"}},
		DisabledTools: []string{"bash"},
		Disabled:      &disabled,
	}
	body := "You are a reviewer.\nBe thorough."

	rendered, err := RenderAgentFile(fm, body)
	require.NoError(t, err)
	require.Contains(t, string(rendered), "description: 'Use when: reviewing API changes'", "a colon in a value must be quoted, not corrupt the frontmatter")
	require.Contains(t, string(rendered), "allowed_tools:\n    - view\n    - grep\n", "allowed_tools must render as a bare list, not the internal struct shape")

	agent, err := ParseAgentContent(string(rendered))
	require.NoError(t, err)
	require.Equal(t, "Reviewer", agent.Name)
	require.Equal(t, "Use when: reviewing API changes", agent.Description)
	require.Equal(t, AgentModeSubagent, agent.Mode)
	require.Equal(t, SelectedModelTypeSmall, agent.Model)
	require.InDelta(t, 0.3, *agent.Temperature, 0.0001)
	require.Equal(t, &AllowedToolSet{Kind: ToolSetScope, Tools: []string{"view", "grep"}}, agent.AllowedTools)
	require.Equal(t, []string{"bash"}, agent.DisabledTools)
	require.False(t, *agent.Disabled)
	require.Equal(t, body, agent.Prompt)
}

func TestRenderAgentFile_AllowedToolsAll(t *testing.T) {
	rendered, err := RenderAgentFile(AgentFrontmatter{
		Description:  "x",
		AllowedTools: &AllowedToolSet{Kind: ToolSetAll},
	}, "body")
	require.NoError(t, err)
	require.Contains(t, string(rendered), "allowed_tools: all\n")

	agent, err := ParseAgentContent(string(rendered))
	require.NoError(t, err)
	require.Equal(t, &AllowedToolSet{Kind: ToolSetAll}, agent.AllowedTools)
}

func TestRenderAgentFile_OmitsUnsetAllowedTools(t *testing.T) {
	rendered, err := RenderAgentFile(AgentFrontmatter{Description: "x"}, "body")
	require.NoError(t, err)
	require.NotContains(t, string(rendered), "allowed_tools")

	agent, err := ParseAgentContent(string(rendered))
	require.NoError(t, err)
	require.Nil(t, agent.AllowedTools)
}

// TestParseAgentContent_RejectsUnknownFrontmatterField is the reason
// frontmatter is parsed with KnownFields. A typo used to be dropped in
// silence, so `allowed_tool:` (missing the s) read as "no restriction
// stated" and the agent quietly ran with a wider tool set than the
// author wrote down.
func TestParseAgentContent_RejectsUnknownFrontmatterField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{"singular allowed_tool", "---\ndescription: x\nallowed_tool:\n  - view\n---\nbody"},
		{"misspelled disabled_tools", "---\ndescription: x\ndisable_tools:\n  - bash\n---\nbody"},
		{"unknown field entirely", "---\ndescription: x\nsuperuser: true\n---\nbody"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseAgentContent(tt.content)
			require.Error(t, err, "an unknown frontmatter field must be a hard error")
		})
	}
}

func TestParseAgentContent_RejectsInvalidFieldValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
	}{
		{"mode all was removed", "---\ndescription: x\nmode: all\n---\nbody"},
		{"unknown mode", "---\ndescription: x\nmode: primray\n---\nbody"},
		{"unknown model", "---\ndescription: x\nmodel: medium\n---\nbody"},
		{"NaN temperature", "---\ndescription: x\ntemperature: .nan\n---\nbody"},
		{"infinite temperature", "---\ndescription: x\ntemperature: .inf\n---\nbody"},
		{"temperature above range", "---\ndescription: x\ntemperature: 1.5\n---\nbody"},
		{"temperature below range", "---\ndescription: x\ntemperature: -0.1\n---\nbody"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseAgentContent(tt.content)
			require.Error(t, err)
		})
	}
}

func TestParseAgentContent_ReadsAllowedMCP(t *testing.T) {
	t.Parallel()

	agent, err := ParseAgentContent("---\ndescription: x\nallowed_mcp:\n  github:\n    - create_issue\n---\nbody")
	require.NoError(t, err)
	require.True(t, agent.AllowedMCP.Allows("github", "create_issue"))
	require.False(t, agent.AllowedMCP.Allows("github", "delete_repo"))

	inherited, err := ParseAgentContent("---\ndescription: x\nallowed_mcp: inherited\n---\nbody")
	require.NoError(t, err)
	require.Equal(t, &AllowedMCPSet{Kind: ToolSetInherited}, inherited.AllowedMCP)
}

func TestValidateAgent_RejectsInvalidValues(t *testing.T) {
	t.Parallel()

	nan := math.NaN()
	inf := math.Inf(1)
	tooHot := 1.5
	ok := 0.5

	require.NoError(t, ValidateAgent("reviewer", Agent{Temperature: &ok, Mode: AgentModeSubagent}))
	require.Error(t, ValidateAgent("Reviewer!", Agent{}), "an invalid id must be rejected")
	require.Error(t, ValidateAgent("reviewer", Agent{Temperature: &nan}))
	require.Error(t, ValidateAgent("reviewer", Agent{Temperature: &inf}))
	require.Error(t, ValidateAgent("reviewer", Agent{Temperature: &tooHot}))
	require.Error(t, ValidateAgent("reviewer", Agent{Mode: AgentMode("all")}))
	require.Error(t, ValidateAgent("reviewer", Agent{Model: SelectedModelType("medium")}))
}
