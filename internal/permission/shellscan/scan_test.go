package shellscan

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const workDir = "/work/project"

// TestKnownBypassesAreNotSafe pins every escape route the previous
// prefix-and-metacharacter check let through. Each case must fail
// SafeInDir, either by decomposing into a blocked segment or by being
// opaque.
func TestKnownBypassesAreNotSafe(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		command string
	}{
		{"output redirection escapes the write check", "echo evil > ~/.bashrc"},
		{"append redirection escapes the write check", "echo evil >> ~/.bashrc"},
		{"bare ampersand chains a second command", "echo x & rm -rf /tmp/y"},
		{"newline separates a second command", "echo hi\nrm -rf x"},
		{"semicolon chains a second command", "ls; rm -rf /"},
		{"and-list chains a second command", "ls && rm -rf /"},
		{"or-list chains a second command", "ls || rm -rf /"},
		{"pipe chains a second command", "ls | xargs rm"},
		{"command substitution hides a command", "ls $(rm -rf /)"},
		{"backtick substitution hides a command", "ls `rm -rf /`"},
		{"process substitution hides a command", "diff <(ls) <(rm -rf /)"},
		{"kill is not a read-only command", "kill -9 123"},
		{"killall is not a read-only command", "killall foo"},
		{"timeout carries an arbitrary command", "timeout 5 rm -rf x"},
		{"nohup carries an arbitrary command", "nohup rm -rf x"},
		{"nice carries an arbitrary command", "nice -n 10 rm -rf x"},
		{"env carries an arbitrary command", "/usr/bin/env timeout 5 rm -rf x"},
		{"env sets a hijacking variable", "env LD_PRELOAD=/x ls"},
		{"env split-string rewrites argv", `env -S "ls -l"`},
		{"prefix assignment hijacks the environment", "LD_PRELOAD=/x ls"},
		{"export mutates the shell environment", "export PATH=/evil"},
		{"subshell hides a command", "(ls)"},
		{"conditional hides a command", "if true; then rm -rf /; fi"},
		{"loop hides a command", "for f in *; do rm $f; done"},
		{"function declaration hides a command", "f() { rm -rf /; }"},
		{"time clause carries a command", "time rm -rf x"},
		{"sudo is an exec vehicle", "sudo ls"},
		{"xargs is an exec vehicle", "xargs rm"},
		{"sh -c is an exec vehicle", "sh -c 'rm -rf /'"},
		{"find -exec is an exec vehicle", `find . -exec rm {} \;`},
		{"python is an exec vehicle", "python3.12 -c 'import os'"},
		{"git pager option runs a program", "git grep -O touch-evil TODO"},
		{"git -c overrides configuration", "git -c core.pager=evil status"},
		{"rg preprocessor runs a program", "rg --pre ./pre.sh TODO"},
		{"sort compress program runs a program", "sort --compress-program=evil f"},
		{"ps dumps process environments", "ps auxe"},
		{"git push publishes code", "git push origin main"},
		{"reading outside the working dir", "cat /etc/passwd"},
		{"listing outside the working dir", "ls /etc"},
		{"chdir escapes the working dir", "cd /etc && cat passwd"},
		{"relative escape from the working dir", "cat ../../etc/passwd"},
		{"home-relative read", "cat ~/.ssh/id_rsa"},
		{"recursive grep cannot pin operands", "grep -r secret ."},
		{"bare rg cannot pin operands", "rg secret"},
		{"writing inside the working dir still asks", "tee out.txt"},
		{"unparsable input fails closed", "ls '"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.False(t, Scan(tc.command, workDir).Safe(insideWorkDir))
		})
	}
}

func TestReadOnlyCommandsAreSafe(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		command string
	}{
		{"listing the working dir", "ls -la"},
		{"listing a subdirectory", "ls internal/permission"},
		{"reading a project file", "cat foo.txt"},
		{"glob operand anchors inside the working dir", "cat *.go"},
		{"git status", "git status"},
		{"git log with options", "git log --oneline -10"},
		{"git config read", "git config --get user.name"},
		{"ps without the environment selector", "ps aux"},
		{"discarding output", "ls > /dev/null"},
		{"descriptor duplication touches no file", "ls 2>&1"},
		{"timeout around a safe command", "timeout 5 ls"},
		{"nice around a safe command", "nice -n 10 ls"},
		{"chained safe commands", "pwd && ls -la"},
		{"piped safe commands", "cat foo.txt | grep bar"},
		{"chdir inside the working dir", "cd internal && ls"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := Scan(tc.command, workDir)
			require.False(t, r.Opaque, "unexpectedly opaque: %s", r.Reason)
			require.True(t, r.Safe(insideWorkDir))
		})
	}
}

func TestScanSplitsChainsIntoSegments(t *testing.T) {
	t.Parallel()

	r := Scan("ls -la; git status && cat foo.txt", workDir)
	require.False(t, r.Opaque)
	require.Len(t, r.Segments, 3)
	require.Equal(t, []string{"ls", "-la"}, r.Segments[0].Words)
	require.Equal(t, []string{"git", "status"}, r.Segments[1].Words)
	require.Equal(t, []string{"cat", "foo.txt"}, r.Segments[2].Words)
}

func TestDangerousVerbIsFlaggedPerSegment(t *testing.T) {
	t.Parallel()

	r := Scan("ls -la && rm -rf build", workDir)
	require.False(t, r.Opaque)
	require.Len(t, r.Segments, 2)
	require.False(t, r.Segments[0].Dangerous)
	require.True(t, r.Segments[1].Dangerous)
}

// TestWrapperStrippingExposesTheRealCommand pins that a wrapper is
// peeled before classification, so the wrapper name can never stand in
// for the command it carries.
func TestWrapperStrippingExposesTheRealCommand(t *testing.T) {
	t.Parallel()

	cases := []struct {
		command string
		words   []string
	}{
		{"timeout 5 ls -la", []string{"ls", "-la"}},
		{"timeout --signal=TERM 5 ls", []string{"ls"}},
		{"timeout -k 1 5 ls", []string{"ls"}},
		{"nice -n 10 ls", []string{"ls"}},
		{"ionice -c 3 ls", []string{"ls"}},
		{"chrt 50 ls", []string{"ls"}},
		{"stdbuf -o0 ls", []string{"ls"}},
		{"nohup ls", []string{"ls"}},
		{"/usr/bin/env ls", []string{"ls"}},
		{"exec ls", []string{"ls"}},
		{"timeout 5 nohup nice -n 5 ls", []string{"ls"}},
	}

	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			t.Parallel()
			r := Scan(tc.command, workDir)
			require.False(t, r.Opaque, "unexpectedly opaque: %s", r.Reason)
			require.Len(t, r.Segments, 1)
			require.Equal(t, tc.words, r.Segments[0].Words)
		})
	}
}

func TestRedirectionIsModelledAsAWrite(t *testing.T) {
	t.Parallel()

	r := Scan("echo hi > out.txt", workDir)
	require.False(t, r.Opaque)
	require.Len(t, r.Segments, 1)
	require.Equal(t,
		[]FileRef{{Path: workDir + "/out.txt", Write: true}},
		r.Segments[0].Files,
	)
}

func TestSafeWriteSinksAreIgnored(t *testing.T) {
	t.Parallel()

	for _, sink := range []string{"/dev/null", "/dev/stdout", "/dev/stderr"} {
		r := Scan("echo hi > "+sink, workDir)
		require.False(t, r.Opaque)
		require.Len(t, r.Segments, 1)
		require.Empty(t, r.Segments[0].Files)
	}
}

func TestFileOperandsAreClassified(t *testing.T) {
	t.Parallel()

	cases := []struct {
		command string
		files   []FileRef
	}{
		{"cat foo.txt", []FileRef{{Path: workDir + "/foo.txt"}}},
		{"cp a.txt b.txt", []FileRef{
			{Path: workDir + "/a.txt"},
			{Path: workDir + "/b.txt", Write: true},
		}},
		{"mv a.txt /tmp/b.txt", []FileRef{
			{Path: workDir + "/a.txt"},
			{Path: "/tmp/b.txt", Write: true},
		}},
		{"rm -rf build", []FileRef{{Path: workDir + "/build", Write: true}}},
		{"mkdir -p out", []FileRef{{Path: workDir + "/out", Write: true}}},
		{"touch f", []FileRef{{Path: workDir + "/f", Write: true}}},
		{"dd if=/dev/zero of=/tmp/x", []FileRef{
			{Path: "/dev/zero"},
			{Path: "/tmp/x", Write: true},
		}},
		{"uniq in.txt out.txt", []FileRef{
			{Path: workDir + "/in.txt"},
			{Path: workDir + "/out.txt", Write: true},
		}},
		{"grep pattern foo.txt", []FileRef{{Path: workDir + "/foo.txt"}}},
		{"tee out.txt", []FileRef{{Path: workDir + "/out.txt", Write: true}}},
		// The end-of-options marker keeps a dash-leading path visible.
		{"rm -- -weird", []FileRef{{Path: workDir + "/-weird", Write: true}}},
	}

	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			t.Parallel()
			r := Scan(tc.command, workDir)
			require.False(t, r.Opaque, "unexpectedly opaque: %s", r.Reason)
			require.Len(t, r.Segments, 1)
			require.Equal(t, tc.files, r.Segments[0].Files)
		})
	}
}

// TestGlobOperandAnchorsToItsDirectory pins that a wildcard still names
// a location, so it neither escapes the scope check nor forces a prompt
// for an ordinary in-project glob.
func TestGlobOperandAnchorsToItsDirectory(t *testing.T) {
	t.Parallel()

	cases := []struct {
		command string
		path    string
	}{
		{"cat *.go", workDir + "/."},
		{"cat src/*.go", workDir + "/src"},
		{"cat /etc/*.conf", "/etc"},
		{"cat ../*.go", workDir + "/.."},
	}

	for _, tc := range cases {
		t.Run(tc.command, func(t *testing.T) {
			t.Parallel()
			r := Scan(tc.command, workDir)
			require.False(t, r.Opaque, "unexpectedly opaque: %s", r.Reason)
			require.Len(t, r.Segments, 1)
			require.Equal(t, []FileRef{{Path: tc.path}}, r.Segments[0].Files)
		})
	}
}

func TestChdirRepointsLaterOperands(t *testing.T) {
	t.Parallel()

	r := Scan("cd /etc && cat passwd", workDir)
	require.False(t, r.Opaque)
	require.Len(t, r.Segments, 2)
	require.Equal(t, []FileRef{{Path: "/etc/passwd"}}, r.Segments[1].Files)
}

func TestSafePrefixScopesGrants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		words  []string
		prefix int
	}{
		{"plain safe command", []string{"ls", "-la"}, 1},
		{"git verb", []string{"git", "status"}, 2},
		{"git verb after a safe global", []string{"git", "--no-pager", "log"}, 3},
		{"unknown command", []string{"npm", "run", "build"}, 0},
		{"git write verb", []string{"git", "commit", "-m", "x"}, 0},
		{"git branch delete", []string{"git", "branch", "-D", "x"}, 0},
		{"git remote add", []string{"git", "remote", "add", "x", "y"}, 0},
		{"git config write", []string{"git", "config", "user.name", "x"}, 0},
		{"empty words", nil, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.prefix, SafePrefix(tc.words))
		})
	}
}

// TestCommandsThatLeaveTheMachineAreNotSafe pins that a command is not
// waved through just because it only reads. The scan models what a
// command does to the filesystem and nothing else, so a request that
// travels — to a cluster, to a git remote, to a resolver — is a
// question it cannot answer and must not answer for the user.
func TestCommandsThatLeaveTheMachineAreNotSafe(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"kubectl get pods",
		"kubectl get secrets -o yaml",
		"kubectl logs some-pod",
		"kubectl describe pod x",
		"kubectl version",
		"git ls-remote https://private.example/repo",
		"git ls-remote --heads origin",
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			r := Scan(command, workDir)
			require.False(t, r.Safe(insideWorkDir),
				"%q reaches the network and must reach the user too", command)
			require.Equal(t, 0, SafePrefix(strings.Fields(command)),
				"%q must not mint a grant that skips the next prompt", command)
		})
	}
}

// TestLocalGitReadsStaySafe pins the other side: taking ls-remote out
// must not cost the ordinary local git reads their quiet path.
func TestLocalGitReadsStaySafe(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"git status",
		"git log --oneline -10",
		"git diff HEAD",
		"git ls-files",
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			require.True(t, Scan(command, workDir).Safe(insideWorkDir))
		})
	}
}

func TestExecVehiclesAreFlagged(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"sudo ls", "sh script.sh", "bash -c ls", "npx cowsay hi",
		"python3 script.py", "node index.js", "docker run alpine",
		"xargs ls", "ssh host ls", "find . -delete",
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			r := Scan(command, workDir)
			require.False(t, r.Opaque, "unexpectedly opaque: %s", r.Reason)
			require.Len(t, r.Segments, 1)
			require.True(t, r.Segments[0].Vehicle)
		})
	}
}

func TestOpaqueResultsCarryAReason(t *testing.T) {
	t.Parallel()

	r := Scan("ls $(whoami)", workDir)
	require.True(t, r.Opaque)
	require.NotEmpty(t, r.Reason)
	require.Empty(t, r.Segments)
}

// TestFilesNamedByAnOptionAreTracked pins that a path a command opens
// through an option reaches the rules. The positional scan skips option
// words, and for a pattern-first command it then drops the first
// operand as the pattern — so `grep -f /outside/pat.txt local.txt` used
// to report a lone read of local.txt while grep also opened a file the
// user never allowed.
func TestFilesNamedByAnOptionAreTracked(t *testing.T) {
	t.Parallel()

	paths := func(command string) []string {
		r := Scan(command, workDir)
		require.False(t, r.Opaque, "unexpectedly opaque: %s", r.Reason)
		require.Len(t, r.Segments, 1)
		var out []string
		for _, f := range r.Segments[0].Files {
			out = append(out, f.Path)
		}
		return out
	}

	t.Run("separated value", func(t *testing.T) {
		t.Parallel()
		require.Contains(t, paths("grep -f /outside/pat.txt local.txt"),
			"/outside/pat.txt")
	})

	t.Run("joined long value", func(t *testing.T) {
		t.Parallel()
		require.Contains(t, paths("grep --file=/outside/pat.txt local.txt"),
			"/outside/pat.txt")
	})

	t.Run("the operand is no longer eaten as the pattern", func(t *testing.T) {
		t.Parallel()
		require.Contains(t, paths("grep -f pat.txt local.txt"),
			workDir+"/local.txt")
	})

	t.Run("files0-from is a read too", func(t *testing.T) {
		t.Parallel()
		require.Contains(t, paths("wc --files0-from=/outside/list"),
			"/outside/list")
	})
}

// TestOptionFilesOutsideTheWorkspaceAreNotSafe is the same defect seen
// from the gate's side: the command must lose its quiet path.
func TestOptionFilesOutsideTheWorkspaceAreNotSafe(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"grep -f /outside/pat.txt local.txt",
		"grep --file=/outside/pat.txt local.txt",
		"rg -f /outside/pat.txt",
		"wc --files0-from=/outside/list",
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			require.False(t, Scan(command, workDir).Safe(insideWorkDir))
		})
	}
}

// TestUnparsableFileOptionsAreNotSafe pins the fail-closed half: a
// value glued to its short option cannot be split off without knowing
// the command, and a file the scan cannot name is one it must not
// vouch for.
func TestUnparsableFileOptionsAreNotSafe(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"grep -f/outside/pat.txt local.txt",
		"grep -fpat.txt local.txt",
		"grep local.txt -f",
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			require.False(t, Scan(command, workDir).Safe(insideWorkDir))
		})
	}
}

// TestPatternFirstReadsStaySafe pins the other side: the option table
// must not cost an ordinary search inside the workspace its quiet path.
func TestPatternFirstReadsStaySafe(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"grep pattern local.txt",
		"grep -i pattern local.txt",
		"grep -f pat.txt local.txt",
		"rg pattern src",
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			require.True(t, Scan(command, workDir).Safe(insideWorkDir))
		})
	}
}

// TestGitReadsCannotLeaveTheCheckout pins the hole --no-index opens.
// The scan has no git operand model, so a git verb reports no files at
// all; that is sound only while git confines itself to the repository.
// --no-index drops that confinement and turns diff and grep into plain
// file readers, which used to run unprompted on any path the process
// could open.
func TestGitReadsCannotLeaveTheCheckout(t *testing.T) {
	t.Parallel()

	for _, command := range []string{
		"git diff --no-index /dev/null /etc/hostname",
		"git diff --no-index /etc/passwd /etc/group",
		"git grep --no-index pattern /etc",
		"git grep -f /outside/pat.txt",
	} {
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			require.False(t, Scan(command, workDir).Safe(insideWorkDir))
		})
	}
}

// insideWorkDir is the containment predicate the scan tests run with.
// Safe takes it as a parameter because resolving a path honestly needs
// the filesystem, which this package deliberately never touches.
func insideWorkDir(path string) bool {
	return withinDir(workDir, path)
}
