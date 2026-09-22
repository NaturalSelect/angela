package tools

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/permission/shellscan"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

//go:embed git.md
var gitDescription string

// gitReadOnlyVerbsHelp mirrors shellscan's own read-only verb list, for
// the error message shown when a call is rejected. It is not the
// enforcement — shellscan.SafePrefix is — just the hint that keeps the
// model from guessing blindly after a rejection.
const gitReadOnlyVerbsHelp = "blame, describe, diff, grep, log, ls-files, rev-parse, shortlog, show, status, " +
	"plus a listing form of branch, tag, remote, or config"

type GitParams struct {
	Args []string `json:"args" description:"Arguments after 'git', one per array element (no shell parsing, so no pipes or redirects). The first element must be a read-only verb, e.g. [\"log\",\"--oneline\",\"-n\",\"20\",\"--\",\"path\"]."`
}

// NewGitTool returns a tool that runs a read-only git command directly
// via argv, without a shell. It exists so a tool set that holds no Bash
// can still answer a git-history question: explore's read-only
// guarantee comes from Bash not being in its tool set at all, and adding
// Bash back just to reach git would trade that structural guarantee for
// a permission-gate one. Restricting the verb here reuses
// shellscan.SafePrefix, the same read-only classifier the permission
// gate already applies to a bash-run git command, so the two never
// drift into judging the same command differently.
func NewGitTool(workingDir string) fantasy.AgentTool {
	return NewTool(
		toolnames.Git,
		gitDescription,
		func(ctx context.Context, params GitParams, _ fantasy.ToolCall) Result {
			if len(params.Args) == 0 {
				return Fail("missing args")
			}
			// The first word must be the verb itself, with no global
			// option ahead of it. This is stricter than SafePrefix
			// needs to be — it also catches a glued short option (like
			// -Ofile) that SafePrefix's exact-match list would miss —
			// and it costs nothing because every allowed verb here is
			// legal as args[0].
			if strings.HasPrefix(params.Args[0], "-") {
				return Fail("args[0] must be a git verb, not an option; global flags such as -C are not allowed")
			}

			words := append([]string{"git"}, params.Args...)
			if shellscan.SafePrefix(words) == 0 {
				return Failf("git %s is not a read-only command; allowed verbs are %s",
					strings.Join(params.Args, " "), gitReadOnlyVerbsHelp)
			}

			argv := append([]string{"--no-pager", "--no-optional-locks"}, params.Args...)
			cmd := exec.CommandContext(ctx, "git", argv...)
			cmd.Dir = workingDir
			// --no-optional-locks keeps a command like `status` or
			// `diff` from refreshing and writing the index; the rest
			// close off a network prompt and a pager that would just
			// hang waiting for a terminal that is not there.
			cmd.Env = append(os.Environ(),
				"GIT_TERMINAL_PROMPT=0",
				"GIT_OPTIONAL_LOCKS=0",
				"GIT_PAGER=cat",
			)

			out, err := cmd.CombinedOutput()
			output := truncateOutput(string(out))
			if exitErr, ok := err.(*exec.ExitError); ok {
				if output != "" {
					output += "\n"
				}
				return Fail(output + fmt.Sprintf("Exit code %d", exitErr.ExitCode()))
			}
			if err != nil {
				return FailErr("error running git", err)
			}
			if output == "" {
				return Ok(BashNoOutput)
			}
			return Ok(output)
		},
	)
}
