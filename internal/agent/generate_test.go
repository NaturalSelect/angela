package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/stretchr/testify/require"
)

// newGenerateTestCoordinator builds a coordinator with just enough
// state for writeGeneratedAgent: a config store with built-in agents
// resolved, pointed at a real (test-scoped) working directory.
func newGenerateTestCoordinator(t *testing.T) *coordinator {
	t.Helper()
	env := testEnv(t)

	cfg, err := config.Init(env.workingDir, "", false)
	require.NoError(t, err)
	cfg.SetupAgents()

	return &coordinator{cfg: cfg}
}

// TestWriteGeneratedAgent_RejectsPathTraversal pins H8: an
// LLM-produced identifier that looks like a path must never let the
// generated file land outside the generated agents directory.
func TestWriteGeneratedAgent_RejectsPathTraversal(t *testing.T) {
	tests := []string{"../../evil", "a/b", "../evil", "/etc/passwd"}
	for _, id := range tests {
		t.Run(id, func(t *testing.T) {
			coord := newGenerateTestCoordinator(t)
			_, _, err := coord.writeGeneratedAgent(GeneratedAgent{
				Identifier:   id,
				WhenToUse:    "Use when testing",
				SystemPrompt: "You are a test agent.",
			})
			require.Error(t, err)
		})
	}
}

// TestWriteGeneratedAgent_RejectsCollisionWithBuiltin pins M5: an
// identifier that collides with an existing agent (here, a built-in
// one) must be rejected in code, not merely discouraged in the
// prompt.
func TestWriteGeneratedAgent_RejectsCollisionWithBuiltin(t *testing.T) {
	coord := newGenerateTestCoordinator(t)
	_, _, err := coord.writeGeneratedAgent(GeneratedAgent{
		Identifier:   config.AgentTask,
		WhenToUse:    "Use when testing",
		SystemPrompt: "You are a test agent.",
	})
	require.ErrorContains(t, err, "already exists")
}

// TestWriteGeneratedAgent_SelfValidatesColonInDescription pins M6: a
// WhenToUse containing a YAML-significant character (a colon) must
// still round-trip through RenderAgentFile -> ParseAgentContent
// instead of corrupting the frontmatter.
func TestWriteGeneratedAgent_SelfValidatesColonInDescription(t *testing.T) {
	coord := newGenerateTestCoordinator(t)
	agent, path, err := coord.writeGeneratedAgent(GeneratedAgent{
		Identifier:   "reviewer",
		WhenToUse:    "Use when: reviewing API changes",
		SystemPrompt: "You are a reviewer.",
	})
	require.NoError(t, err)
	require.Equal(t, "Use when: reviewing API changes", agent.Description)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	parsed, err := config.ParseAgentContent(string(raw))
	require.NoError(t, err)
	require.Equal(t, "Use when: reviewing API changes", parsed.Description)
}

// TestWriteGeneratedAgent_SecondCallWithSameIdentifierFails pins the
// M4 TOCTOU fix: a second call with the same identifier must fail via
// O_EXCL, and the first file's content must survive untouched.
func TestWriteGeneratedAgent_SecondCallWithSameIdentifierFails(t *testing.T) {
	coord := newGenerateTestCoordinator(t)
	_, path, err := coord.writeGeneratedAgent(GeneratedAgent{
		Identifier:   "reviewer",
		WhenToUse:    "Use when testing",
		SystemPrompt: "You are a test agent.",
	})
	require.NoError(t, err)

	before, err := os.ReadFile(path)
	require.NoError(t, err)

	_, _, err = coord.writeGeneratedAgent(GeneratedAgent{
		Identifier:   "reviewer",
		WhenToUse:    "A completely different description",
		SystemPrompt: "A completely different prompt.",
	})
	require.ErrorContains(t, err, "already exists")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after, "the first file's content must not be truncated by a failed second write")
}

// TestWriteGeneratedAgent_WritesUnderGeneratedAgentDir pins M4: the
// output path must be config.GeneratedAgentDir(workingDir), not a
// directory guessed by prefix-matching AgentPaths.
func TestWriteGeneratedAgent_WritesUnderGeneratedAgentDir(t *testing.T) {
	coord := newGenerateTestCoordinator(t)
	_, path, err := coord.writeGeneratedAgent(GeneratedAgent{
		Identifier:   "reviewer",
		WhenToUse:    "Use when testing",
		SystemPrompt: "You are a test agent.",
	})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(config.GeneratedAgentDir(coord.cfg.WorkingDir()), "reviewer.md"), path)
}

// TestWriteGeneratedAgent_RejectsEmptyFields covers a model that
// returns the right shape with nothing in it. A blank description or
// system prompt produces an agent that is dispatchable but useless, and
// the blank is far easier to diagnose here than at first dispatch.
func TestWriteGeneratedAgent_RejectsEmptyFields(t *testing.T) {
	tests := []struct {
		name      string
		generated GeneratedAgent
	}{
		{"empty description", GeneratedAgent{Identifier: "reviewer", SystemPrompt: "You review."}},
		{"blank description", GeneratedAgent{Identifier: "reviewer", WhenToUse: "   \n", SystemPrompt: "You review."}},
		{"empty prompt", GeneratedAgent{Identifier: "reviewer", WhenToUse: "Use when reviewing"}},
		{"blank prompt", GeneratedAgent{Identifier: "reviewer", WhenToUse: "Use when reviewing", SystemPrompt: "  "}},
		{"blank identifier", GeneratedAgent{Identifier: "  ", WhenToUse: "Use when reviewing", SystemPrompt: "You review."}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coord := newGenerateTestCoordinator(t)
			_, _, err := coord.writeGeneratedAgent(tt.generated)
			require.Error(t, err)

			dir := config.GeneratedAgentDir(coord.cfg.WorkingDir())
			entries, readErr := os.ReadDir(dir)
			if readErr == nil {
				require.Empty(t, entries, "a rejected agent must leave nothing behind")
			}
		})
	}
}

// TestWriteGeneratedAgent_RejectsUnparsableSystemPrompt covers a model
// that emits Go template syntax in the prompt. Such a file parses as
// markdown but explodes the first time the agent is dispatched, so it
// must be caught before it reaches disk.
func TestWriteGeneratedAgent_RejectsUnparsableSystemPrompt(t *testing.T) {
	coord := newGenerateTestCoordinator(t)

	_, _, err := coord.writeGeneratedAgent(GeneratedAgent{
		Identifier:   "reviewer",
		WhenToUse:    "Use when reviewing",
		SystemPrompt: "You review {{.Unclosed code.",
	})
	require.ErrorContains(t, err, "not a valid template")

	_, statErr := os.Stat(filepath.Join(config.GeneratedAgentDir(coord.cfg.WorkingDir()), "reviewer.md"))
	require.True(t, os.IsNotExist(statErr), "a broken prompt must not be written")
}

// TestWriteGeneratedAgent_LeavesNoTempFileBehind pins the atomic write:
// after a successful write only the final file remains, and after a
// rejected one the directory is untouched. A leftover .agent-*.md would
// not be discovered as an agent but would accumulate in the user's repo.
func TestWriteGeneratedAgent_LeavesNoTempFileBehind(t *testing.T) {
	coord := newGenerateTestCoordinator(t)
	dir := config.GeneratedAgentDir(coord.cfg.WorkingDir())

	_, _, err := coord.writeGeneratedAgent(GeneratedAgent{
		Identifier:   "reviewer",
		WhenToUse:    "Use when reviewing",
		SystemPrompt: "You review.",
	})
	require.NoError(t, err)

	// A second write of the same identifier fails at publish time,
	// after its temp file already exists.
	_, _, err = coord.writeGeneratedAgent(GeneratedAgent{
		Identifier:   "reviewer",
		WhenToUse:    "Use when reviewing again",
		SystemPrompt: "You review again.",
	})
	require.Error(t, err)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	require.Equal(t, []string{"reviewer.md"}, names, "no temp files may survive")
}

// TestWriteGeneratedAgent_RejectsSymlinkedAgentDir stops a linked
// .angela/agents from redirecting generated prompts outside the
// workspace the user is actually working in.
func TestWriteGeneratedAgent_RejectsSymlinkedAgentDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs elevated privileges on Windows")
	}

	coord := newGenerateTestCoordinator(t)
	dir := config.GeneratedAgentDir(coord.cfg.WorkingDir())
	outside := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o755))
	require.NoError(t, os.Symlink(outside, dir))

	_, _, err := coord.writeGeneratedAgent(GeneratedAgent{
		Identifier:   "reviewer",
		WhenToUse:    "Use when reviewing",
		SystemPrompt: "You review.",
	})
	require.ErrorContains(t, err, "outside the workspace")

	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	require.Empty(t, entries, "nothing may be written through the symlink")
}
