package agent

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/stretchr/testify/require"
)

var spilledPathPattern = regexp.MustCompile(`saved to (\S+\.txt)\.`)

// The recorded-session tests build Bash without the wrappers, so they
// never spill. This runs the real Bash tool through the chain buildTools
// assembles, the way a session does.
func TestBuiltBashToolSpillsOversizedOutput(t *testing.T) {
	coord := newGateTestCoordinator(t, false)

	toolList, err := coord.buildTools(coord.cfg.Config().Agents[config.AgentCoder], config.ActiveAgent{}, "", 0)
	require.NoError(t, err)

	var bash fantasy.AgentTool
	for _, tool := range toolList {
		if tool.Info().Name == toolnames.Bash {
			bash = tool
		}
	}
	require.NotNil(t, bash)

	ctx := context.WithValue(t.Context(), tools.SessionIDContextKey, "session-chain")
	resp, err := bash.Run(ctx, fantasy.ToolCall{
		ID: "call-1", Name: toolnames.Bash, Input: `{"description":"count","command":"seq 1 20000"}`,
	})
	require.NoError(t, err)
	require.False(t, resp.IsError, resp.Content)

	match := spilledPathPattern.FindStringSubmatch(resp.Content)
	require.NotNil(t, match, "the result must say where the full output went")
	path := match[1]
	require.True(t, strings.HasPrefix(path, ToolOutputRoot(coord.cfg.Config().Options.DataDirectory)),
		"the file must live under the tool-output root the permission service treats as readable")

	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	// NOTE: require.Contains would print the whole 100 KB string on failure.
	require.True(t, strings.Contains(string(saved), "\n10000\n"), "the file must hold the middle that the preview drops")
	require.False(t, strings.Contains(resp.Content, "\n10000\n"), "test premise: the preview omits the middle")
	require.True(t, strings.HasPrefix(resp.Content, "1\n2\n3\n"), "the preview keeps the beginning")
	require.True(t, strings.Contains(resp.Content, "\n20000\n"), "the preview keeps the end")
}
