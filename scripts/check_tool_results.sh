#!/bin/bash
# Every fantasy tool must be built through tools.NewTool / tools.NewParallelTool
# (internal/agent/tools/result.go) instead of calling fantasy.NewAgentTool or
# fantasy.NewParallelAgentTool directly, so tool code has no path that hands
# fantasy a raw Go error. fantasy treats that error slot as a critical,
# turn-ending failure and classifies it the same way as a provider error: a
# permission-denied error satisfies net.Error by coincidence (via
# syscall.Errno's Timeout()/Temporary() methods), so it gets retried with
# exponential backoff instead of being shown to the model as an ordinary tool
# result. See internal/agent/tools/result.go and internal/agent/safe_tool.go.
if grep -rnE 'fantasy\.New(Parallel)?AgentTool\(' --include='*.go' internal/ | grep -v '^internal/agent/tools/result\.go:'; then
  echo "❌ Use tools.NewTool / tools.NewParallelTool instead of calling fantasy.NewAgentTool directly."
  exit 1
fi

# A bare "return fantasy.ToolResponse{}, <err>" hands fantasy a raw Go error
# the same way. The only sanctioned occurrences are the ctx.Err() cancellation
# passthroughs in tools/result.go and permissioned_tool.go — cancellation is
# the one failure that must still leave a tool as a Go error — plus whatever
# _test.go files simulate to exercise the safeTool safety net.
if grep -rnE 'return fantasy\.ToolResponse\{\}, ' --include='*.go' internal/agent/ \
    | grep -v '_test\.go:' \
    | grep -v '^internal/agent/tools/result\.go:' \
    | grep -v '^internal/agent/permissioned_tool\.go:'; then
  echo "❌ Tools must return tools.Result; a bare Go error here becomes a provider retry."
  exit 1
fi
