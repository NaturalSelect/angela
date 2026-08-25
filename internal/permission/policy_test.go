package permission

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const policyCwd = "/work/project"

func mustPolicy(t *testing.T, rules []Rule, legacy []string) *Policy {
	t.Helper()
	p, err := CompilePolicy(rules, legacy, PromptAsk)
	require.NoError(t, err)
	return p
}

func TestPolicyPrecedence(t *testing.T) {
	t.Parallel()

	access := Access{Tool: "view", Action: ActionRead, Path: policyCwd + "/a.go"}

	t.Run("deny outranks allow", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{
			{Action: RuleAllow, Tool: "read"},
			{Action: RuleDeny, Tool: "read"},
		}, nil)
		v := p.Evaluate(access, policyCwd)
		require.True(t, v.Matched)
		require.Equal(t, RuleDeny, v.Action)
	})

	t.Run("deny short-circuits regardless of order", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{
			{Action: RuleDeny, Tool: "read"},
			{Action: RuleAllow, Tool: "read"},
		}, nil)
		require.Equal(t, RuleDeny, p.Evaluate(access, policyCwd).Action)
	})

	t.Run("ask outranks allow", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{
			{Action: RuleAllow, Tool: "read"},
			{Action: RuleAsk, Tool: "read"},
		}, nil)
		require.Equal(t, RuleAsk, p.Evaluate(access, policyCwd).Action)
	})

	t.Run("no rule leaves the access unsettled", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{{Action: RuleDeny, Tool: "edit"}}, nil)
		require.False(t, p.Evaluate(access, policyCwd).Matched)
	})

	t.Run("an empty policy settles nothing", func(t *testing.T) {
		t.Parallel()
		require.False(t, mustPolicy(t, nil, nil).Evaluate(access, policyCwd).Matched)
	})
}

func TestPolicyToolFilter(t *testing.T) {
	t.Parallel()

	view := Access{Tool: "view", Action: ActionRead, Path: policyCwd + "/a.go"}
	grep := Access{Tool: "grep", Action: ActionRead, Path: policyCwd}

	t.Run("a category covers every tool in it", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{{Action: RuleDeny, Tool: "read"}}, nil)
		require.True(t, p.Evaluate(view, policyCwd).Matched)
		require.True(t, p.Evaluate(grep, policyCwd).Matched)
	})

	t.Run("a tool name singles one out", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{{Action: RuleDeny, Tool: "view"}}, nil)
		require.True(t, p.Evaluate(view, policyCwd).Matched)
		require.False(t, p.Evaluate(grep, policyCwd).Matched)
	})

	t.Run("an empty filter covers everything", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{{Action: RuleDeny}}, nil)
		require.True(t, p.Evaluate(view, policyCwd).Matched)
		require.True(t, p.Evaluate(grep, policyCwd).Matched)
	})
}

// TestPolicyPathPatternForms pins that one rule catches a path however
// the tool happened to spell it.
func TestPolicyPathPatternForms(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, []Rule{
		{Action: RuleDeny, Tool: "read", Pattern: "**/.env"},
	}, nil)

	for _, path := range []string{
		policyCwd + "/.env",
		policyCwd + "/config/.env",
		"/elsewhere/.env",
	} {
		require.True(t, p.Evaluate(
			Access{Tool: "view", Action: ActionRead, Path: path}, policyCwd,
		).Matched, "path %q should match", path)
	}

	require.False(t, p.Evaluate(
		Access{Tool: "view", Action: ActionRead, Path: policyCwd + "/.env.example"}, policyCwd,
	).Matched)
}

func TestPolicyPathStarDoesNotCrossSeparators(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, []Rule{
		{Action: RuleDeny, Tool: "edit", Pattern: "secrets/*"},
	}, nil)

	require.True(t, p.Evaluate(
		Access{Tool: "edit", Action: ActionEdit, Path: policyCwd + "/secrets/key"}, policyCwd,
	).Matched)
	require.False(t, p.Evaluate(
		Access{Tool: "edit", Action: ActionEdit, Path: policyCwd + "/secrets/deep/key"}, policyCwd,
	).Matched)
}

// TestPolicyDoesNotInventWorkspaceRelativeSpellings pins that a path
// outside the workspace never acquires a workspace-relative spelling.
// "./**" reads as "anything inside the workspace"; if an outside path
// were anchored to the working directory before matching, that rule
// would quietly allow the whole filesystem.
//
// The anchoring hinges on what counts as absolute, which only differs
// on Windows, so this guard does its real work in CI.
func TestPolicyDoesNotInventWorkspaceRelativeSpellings(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, []Rule{
		{Action: RuleAllow, Tool: "edit", Pattern: "./**"},
	}, nil)

	inside := p.Evaluate(
		Access{Tool: "edit", Action: ActionEdit, Path: policyCwd + "/main.go"}, policyCwd,
	)
	require.True(t, inside.Matched)
	require.Equal(t, RuleAllow, inside.Action, "the workspace is what the rule opened")

	outside := p.Evaluate(
		Access{Tool: "edit", Action: ActionEdit, Path: "/etc/passwd"}, policyCwd,
	)
	require.False(t, outside.Matched,
		"a file outside the workspace must not match a workspace-scoped rule")
}

// TestPolicyBashAllowIsConjunctive pins the trap a free-form pattern
// opens: "ls*" matches the whole string "ls && rm -rf /", so an allow
// must be re-checked against every link of the chain.
func TestPolicyBashAllowIsConjunctive(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, []Rule{
		{Action: RuleAllow, Tool: "execute", Pattern: "ls*"},
	}, nil)

	allowed := p.Evaluate(
		Access{Tool: "bash", Action: ActionExecute, Command: "ls -la"}, policyCwd,
	)
	require.True(t, allowed.Matched)
	require.Equal(t, RuleAllow, allowed.Action)

	for _, command := range []string{
		"ls && rm -rf /",
		"ls; rm -rf /",
		"ls | xargs rm",
		"ls $(rm -rf /)",
	} {
		v := p.Evaluate(
			Access{Tool: "bash", Action: ActionExecute, Command: command}, policyCwd,
		)
		require.False(t, v.Matched, "%q must not be allowed by a rule for ls", command)
	}
}

// TestPolicyBashDenyMatchesAnyLink pins that a deny rule cannot be
// dodged by burying the denied command inside a chain.
func TestPolicyBashDenyMatchesAnyLink(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, []Rule{
		{Action: RuleDeny, Tool: "execute", Pattern: "rm *"},
	}, nil)

	for _, command := range []string{
		"rm -rf build",
		"ls && rm -rf build",
		"pwd; ls; rm -rf build",
	} {
		v := p.Evaluate(
			Access{Tool: "bash", Action: ActionExecute, Command: command}, policyCwd,
		)
		require.True(t, v.Matched, "%q should match the deny rule", command)
		require.Equal(t, RuleDeny, v.Action)
	}
}

// TestPolicyBashDenyNotBypassedByWhitespacePrefix pins that padding a
// command does not move it out of a rule's reach.
func TestPolicyBashDenyNotBypassedByWhitespacePrefix(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, []Rule{
		{Action: RuleDeny, Tool: "execute", Pattern: "rm *"},
	}, nil)

	for _, command := range []string{"   rm -rf build", "\trm -rf build"} {
		v := p.Evaluate(
			Access{Tool: "bash", Action: ActionExecute, Command: command}, policyCwd,
		)
		require.True(t, v.Matched, "%q should match the deny rule", command)
		require.Equal(t, RuleDeny, v.Action)
	}
}

func TestPolicyNetworkPatterns(t *testing.T) {
	t.Parallel()

	t.Run("domain mode covers subdomains", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{
			{Action: RuleAllow, Tool: "network", Pattern: "example.com", Mode: PatternDomain},
		}, nil)

		for _, url := range []string{
			"https://example.com/x",
			"https://api.example.com/x",
			"http://example.com:8080/x",
		} {
			require.True(t, p.Evaluate(
				Access{Tool: "web_fetch", Action: ActionNetwork, URL: url}, policyCwd,
			).Matched, "url %q should match", url)
		}

		require.False(t, p.Evaluate(
			Access{Tool: "web_fetch", Action: ActionNetwork, URL: "https://notexample.com/x"},
			policyCwd,
		).Matched)
	})

	t.Run("free mode globs the whole url", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{
			{Action: RuleAllow, Tool: "network", Pattern: "https://example.com/*"},
		}, nil)
		require.True(t, p.Evaluate(
			Access{Tool: "fetch", Action: ActionNetwork, URL: "https://example.com/a/b"},
			policyCwd,
		).Matched)
	})
}

// TestDomainMatchesTheHostActuallyRequested pins that a domain rule is
// judged on the host the HTTP client will connect to. Userinfo can
// carry both a colon and an @, so splitting the string by hand hands
// the allowed name to a request aimed somewhere else entirely.
func TestDomainMatchesTheHostActuallyRequested(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, []Rule{
		{Action: RuleAllow, Tool: "network", Pattern: "docs.example.com", Mode: PatternDomain},
	}, nil)

	matched := func(rawURL string) bool {
		return p.Evaluate(
			Access{Tool: "web_fetch", Action: ActionNetwork, URL: rawURL}, policyCwd,
		).Matched
	}

	t.Run("userinfo cannot impersonate the allowed host", func(t *testing.T) {
		t.Parallel()
		for _, rawURL := range []string{
			"https://docs.example.com:pass@evil.example/path",
			"https://docs.example.com@evil.example/path",
			"https://user:docs.example.com@evil.example/path",
			"https://docs.example.com%40evil.example@evil.example/",
		} {
			require.False(t, matched(rawURL),
				"%q connects to evil.example and must not match", rawURL)
		}
	})

	t.Run("the real host still matches", func(t *testing.T) {
		t.Parallel()
		for _, rawURL := range []string{
			"https://docs.example.com/path",
			"https://DOCS.EXAMPLE.COM/path",
			"https://docs.example.com./path",
			"https://docs.example.com:8443/path",
			"http://sub.docs.example.com/path",
			"https://user:pass@docs.example.com/path",
		} {
			require.True(t, matched(rawURL), "%q is the allowed host", rawURL)
		}
	})

	t.Run("input that reaches no host matches nothing", func(t *testing.T) {
		t.Parallel()
		for _, rawURL := range []string{
			"",
			"docs.example.com/path",
			"ftp://docs.example.com/path",
			"file:///etc/passwd",
			"how do i use docs.example.com",
			"://docs.example.com",
		} {
			require.False(t, matched(rawURL),
				"%q is not an http(s) url and must not match a domain rule", rawURL)
		}
	})

	t.Run("an ipv6 literal is compared without its brackets", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{
			{Action: RuleAllow, Tool: "network", Pattern: "::1", Mode: PatternDomain},
		}, nil)
		require.True(t, p.Evaluate(
			Access{Tool: "web_fetch", Action: ActionNetwork, URL: "http://[::1]:8080/x"},
			policyCwd,
		).Matched)
	})
}

// TestDenyPathRuleSeesThroughSymlinks pins that a rule naming a real
// location still matches a path that reaches it through a link inside
// the workspace. Scope checks resolve links, so a rule that did not
// would disagree with them — and in skip mode the ladder returns before
// the scope check runs, leaving the deny rule as the only guard.
func TestDenyPathRuleSeesThroughSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(outside, "passwd"), []byte("root:x:0:0"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(workspace, "escape")))

	// Resolved so the rule names the same real directory the link
	// reaches, which is what an administrator would write.
	realOutside, err := filepath.EvalSymlinks(outside)
	require.NoError(t, err)

	p := mustPolicy(t, []Rule{
		{Action: RuleDeny, Tool: "read", Pattern: filepath.ToSlash(realOutside) + "/**"},
	}, nil)

	through := filepath.Join(workspace, "escape", "passwd")

	t.Run("the deny rule matches the path taken through the link", func(t *testing.T) {
		t.Parallel()
		verdict := p.Evaluate(
			Access{Tool: "view", Action: ActionRead, Path: through}, workspace)
		require.True(t, verdict.Matched, "the rule must see where the link lands")
		require.Equal(t, RuleDeny, verdict.Action)
	})

	t.Run("skip mode does not get past the deny rule", func(t *testing.T) {
		t.Parallel()
		svc := NewPermissionService(workspace, true, p)

		decision := svc.Gate(t.Context(), GateRequest{
			SessionID: "s", ToolCallID: "c",
			Access: Access{Tool: "view", Action: ActionRead, Path: through},
		})
		require.Equal(t, OutcomePolicyDeny, decision.Outcome,
			"a deny rule outranks skip mode, link or no link")
	})

	t.Run("an unrelated file in the workspace is untouched", func(t *testing.T) {
		t.Parallel()
		inside := filepath.Join(workspace, "main.go")
		require.NoError(t, os.WriteFile(inside, []byte("package main"), 0o644))

		require.False(t, p.Evaluate(
			Access{Tool: "view", Action: ActionRead, Path: inside}, workspace,
		).Matched)
	})
}

func TestPolicyMCPPatterns(t *testing.T) {
	t.Parallel()

	p := mustPolicy(t, []Rule{
		{Action: RuleAllow, Tool: "mcp", Pattern: "docker/*"},
	}, nil)

	require.True(t, p.Evaluate(Access{
		Tool: "mcp_docker_mcp-find", Action: ActionMCP, Server: "docker", MCPTool: "mcp-find",
	}, policyCwd).Matched)

	require.False(t, p.Evaluate(Access{
		Tool: "mcp_other_thing", Action: ActionMCP, Server: "other", MCPTool: "thing",
	}, policyCwd).Matched)
}

// TestPolicyLegacyAllowedToolsStillDeniable pins that folding the old
// flat allow-list into rules leaves a deny rule able to overrule it.
func TestPolicyLegacyAllowedTools(t *testing.T) {
	t.Parallel()

	access := Access{Tool: "view", Action: ActionRead, Path: policyCwd + "/.env"}

	t.Run("a legacy entry allows the tool", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, nil, []string{"view"})
		v := p.Evaluate(access, policyCwd)
		require.True(t, v.Matched)
		require.Equal(t, RuleAllow, v.Action)
	})

	t.Run("a tool:action entry allows the tool", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, nil, []string{"view:read"})
		require.Equal(t, RuleAllow, p.Evaluate(access, policyCwd).Action)
	})

	t.Run("a deny rule overrules a legacy entry", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{
			{Action: RuleDeny, Tool: "read", Pattern: "**/.env"},
		}, []string{"view"})
		require.Equal(t, RuleDeny, p.Evaluate(access, policyCwd).Action)
	})
}

func TestCompilePolicyRejectsInvalidPatterns(t *testing.T) {
	t.Parallel()

	_, err := CompilePolicy([]Rule{{Action: RuleDeny, Pattern: "["}}, nil, PromptAsk)
	require.Error(t, err)
}

func TestFreeMatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		pattern string
		text    string
		want    bool
	}{
		{"ls*", "ls -la", true},
		{"ls*", "lsof", true},
		{"ls *", "lsof", false},
		{"*", "anything at all", true},
		{"git *", "git status", true},
		{"git *", "gitleaks detect", false},
		{"*rm*", "ls && rm -rf /", true},
		{"a?c", "abc", true},
		{"a?c", "ac", false},
		{"npm run *", "npm run build/x", true},
		{"", "x", false},
		{"", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.pattern+"|"+tc.text, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, freeMatch(tc.pattern, tc.text))
		})
	}
}

func TestActionRoundTrip(t *testing.T) {
	t.Parallel()

	for _, action := range []Action{
		ActionRead, ActionList, ActionEdit, ActionExecute, ActionNetwork, ActionMCP,
	} {
		parsed, ok := ParseAction(action.String())
		require.True(t, ok)
		require.Equal(t, action, parsed)
	}

	for alias, want := range map[string]Action{
		"write": ActionEdit, "fetch": ActionNetwork,
		"search": ActionNetwork, "download": ActionNetwork,
	} {
		parsed, ok := ParseAction(alias)
		require.True(t, ok, "alias %q should parse", alias)
		require.Equal(t, want, parsed)
	}

	_, ok := ParseAction("nonsense")
	require.False(t, ok)
}

// TestPolicyJudgesCommandFileOperands pins that a path rule covers both
// routes to a file. Without this, `deny read **/.env` would stop the
// view tool and wave through `cat .env`, which is the same read.
func TestPolicyJudgesCommandFileOperands(t *testing.T) {
	t.Parallel()

	command := func(c string) Access {
		return Access{Tool: "bash", Action: ActionExecute, Command: c, Path: policyCwd}
	}

	t.Run("a read rule catches a command reading the file", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{{Action: RuleDeny, Tool: "read", Pattern: "**/.env"}}, nil)

		v := p.Evaluate(command("cat .env"), policyCwd)
		require.True(t, v.Matched)
		require.Equal(t, RuleDeny, v.Action)
	})

	t.Run("an edit rule catches a command writing the file", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{{Action: RuleDeny, Tool: "edit", Pattern: "**/.env"}}, nil)

		v := p.Evaluate(command("echo x > .env"), policyCwd)
		require.True(t, v.Matched)
		require.Equal(t, RuleDeny, v.Action)
	})

	t.Run("a deny reaches a file in any link of a chain", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{{Action: RuleDeny, Tool: "read", Pattern: "**/.env"}}, nil)

		v := p.Evaluate(command("ls && cat .env"), policyCwd)
		require.True(t, v.Matched)
		require.Equal(t, RuleDeny, v.Action)
	})

	t.Run("an unrelated file is left alone", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{{Action: RuleDeny, Tool: "read", Pattern: "**/.env"}}, nil)

		require.False(t, p.Evaluate(command("cat README.md"), policyCwd).Matched)
	})

	t.Run("allowing a path does not allow the command", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{{Action: RuleAllow, Tool: "read", Pattern: "**/*.md"}}, nil)

		// The file is allowed, but nothing allowed `cat`, so the
		// command stays unsettled rather than being waved through.
		require.False(t, p.Evaluate(command("cat README.md"), policyCwd).Matched)
	})
}

// TestPolicyJudgesDownloadLanding pins that a download is judged as the
// two things it is. A rule opening a domain must not also hand out the
// filesystem: the destination is a write, and nothing but a write rule
// may approve it.
func TestPolicyJudgesDownloadLanding(t *testing.T) {
	t.Parallel()

	// Absolute paths come from the filesystem so the patterns mean the
	// same thing on Windows, where a leading slash carries no volume.
	workDir := t.TempDir()
	outside := t.TempDir()
	outsideGlob := filepath.ToSlash(outside) + "/**"

	download := func(dir string) Access {
		return Access{
			Tool:   "download",
			Action: ActionNetwork,
			URL:    "https://example.com/payload",
			Path:   filepath.Join(dir, "payload.sh"),
		}
	}

	t.Run("a domain allow does not approve the file it writes", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{
			{Action: RuleAllow, Tool: "network", Pattern: "example.com", Mode: PatternDomain},
		}, nil)

		v := p.Evaluate(download(outside), workDir)
		require.False(t, v.Matched,
			"the landing path was never covered, so the download must still be settled by the user")
	})

	t.Run("a deny on the destination refuses the download", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{
			{Action: RuleAllow, Tool: "network", Pattern: "example.com", Mode: PatternDomain},
			{Action: RuleDeny, Tool: "edit", Pattern: outsideGlob},
		}, nil)

		v := p.Evaluate(download(outside), workDir)
		require.True(t, v.Matched)
		require.Equal(t, RuleDeny, v.Action)
	})

	t.Run("an ask on the destination outranks the domain allow", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{
			{Action: RuleAllow, Tool: "network", Pattern: "example.com", Mode: PatternDomain},
			{Action: RuleAsk, Tool: "edit", Pattern: outsideGlob},
		}, nil)

		require.Equal(t, RuleAsk, p.Evaluate(download(outside), workDir).Action)
	})

	t.Run("covering both legs allows the download", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{
			{Action: RuleAllow, Tool: "network", Pattern: "example.com", Mode: PatternDomain},
			{Action: RuleAllow, Tool: "edit", Pattern: outsideGlob},
		}, nil)

		v := p.Evaluate(download(outside), workDir)
		require.True(t, v.Matched)
		require.Equal(t, RuleAllow, v.Action)
	})

	// Naming the tool covers everything that tool does. This is what
	// the older allowed_tools list compiles to, so breaking it would
	// silently start prompting users who had approved download once.
	t.Run("naming the tool covers both legs", func(t *testing.T) {
		t.Parallel()
		for name, p := range map[string]*Policy{
			"rule":   mustPolicy(t, []Rule{{Action: RuleAllow, Tool: "download"}}, nil),
			"legacy": mustPolicy(t, nil, []string{"download"}),
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				v := p.Evaluate(download(outside), workDir)
				require.True(t, v.Matched)
				require.Equal(t, RuleAllow, v.Action)
			})
		}
	})

	t.Run("a network access that writes nothing keeps its old path", func(t *testing.T) {
		t.Parallel()
		p := mustPolicy(t, []Rule{
			{Action: RuleAllow, Tool: "network", Pattern: "example.com", Mode: PatternDomain},
		}, nil)

		v := p.Evaluate(Access{
			Tool: "web_fetch", Action: ActionNetwork, URL: "https://example.com/x",
		}, workDir)
		require.True(t, v.Matched)
		require.Equal(t, RuleAllow, v.Action, "fetch carries no landing path to judge")
	})
}
