package shellscan

import (
	"runtime"
	"slices"
	"strings"
)

// dangerousCommands destroy data, kill processes or publish code. A
// segment matching one of them must always be shown to the user: no
// stored grant and no allow rule may satisfy it.
var dangerousCommands = []string{
	"chattr",
	"chgrp",
	"chmod",
	"chown",
	"dd",
	"git push",
	"kill",
	"killall",
	"mkfs",
	"pkill",
	"reboot",
	"rm",
	"shutdown",
}

// execVehicles can carry an arbitrary command, so their words say
// nothing about what actually runs. They are never auto-allowed, and a
// grant for one is pinned to the full command.
var execVehicles = []string{
	"ack", "awk", "bash", "brew", "bun", "bunx", "chroot", "dash", "deno",
	"doas", "docker", "fish", "flock", "gawk", "ksh", "make", "mawk",
	"nawk", "npm", "npx", "nsenter", "pipx", "pnpm", "podman", "setsid",
	"sh", "ssh", "su", "sudo", "uv", "uvx", "watch", "xargs", "yarn",
	"zsh",
}

// execVehicleFamilies are interpreters whose binaries carry a version
// suffix, such as python3.12 or node22.
var execVehicleFamilies = []string{"lua", "node", "perl", "php", "python", "ruby"}

// safeCommands read state without changing it. Membership only makes a
// segment eligible to skip the prompt; the operands it touches are
// still checked against the caller's allowed scope.
var safeCommands = []string{
	"cal", "cat", "cd", "date", "df", "du", "echo", "file", "free",
	"grep", "groups", "head", "hostname", "id", "ls", "ps", "pwd", "rg",
	"stat", "tail", "tree", "type", "uname", "uptime", "wc", "whatis",
	"whereis", "which", "whoami",
}

func init() {
	if runtime.GOOS == "windows" {
		// nslookup and ping are absent on purpose: both reach the
		// network, and a name lookup carries whatever the caller puts
		// in the name.
		safeCommands = append(safeCommands,
			"ipconfig", "systeminfo", "tasklist", "where",
		)
	}
	slices.Sort(safeCommands)
}

// gitReadOnlyVerbs query a repository without writing to it.
//
// They also read it without leaving the machine. ls-remote is absent
// for that reason: it contacts the remote, sending whatever credentials
// the environment holds, which is a different question from reading the
// checkout and needs its own answer.
var gitReadOnlyVerbs = []string{
	"blame", "describe", "diff", "grep", "log", "ls-files",
	"rev-parse", "shortlog", "show", "status",
}

// gitUnsafeOptions make git run an external program, point it at a
// different repository, or take operands that are ordinary paths rather
// than things tracked in the checkout.
//
// --no-index is the last of those: it turns `git diff` and `git grep`
// into plain file readers that accept any path on the machine, and the
// scan has no git operand model to notice where those paths lead.
var gitUnsafeOptions = []string{
	"--config-env", "--exec-path", "--ext-diff", "--git-dir",
	"--no-index", "--open-files-in-pager", "--output", "--pager",
	"--receive-pack", "--textconv", "--upload-pack", "--work-tree",
	"-C", "-O", "-c",
}

// gitRefQueryOptions are the branch and tag options that only list or
// filter refs. The set is a whitelist because creation carries no
// option at all — `git branch release` writes a ref with nothing but a
// positional operand — so the question cannot be "does this hold a
// mutating option" but "is every word here one this list explains".
var gitRefQueryOptions = []string{
	"--abbrev", "--all", "--color", "--column", "--contains",
	"--format", "--ignore-case", "--list", "--merged", "--no-abbrev",
	"--no-color", "--no-column", "--no-contains", "--no-merged",
	"--points-at", "--remotes", "--show-current", "--sort", "--verbose",
	"-a", "-i", "-l", "-r", "-v", "-vv",
}

// gitRefQueryValueOptions take the following word as their value, so
// that word is a pattern or a commit rather than a ref being created.
var gitRefQueryValueOptions = []string{
	"--abbrev", "--color", "--column", "--contains", "--format",
	"--list", "--merged", "--no-contains", "--no-merged",
	"--points-at", "--sort", "-l",
}

var gitConfigReadOptions = []string{
	"--get", "--get-all", "--get-regexp", "--list", "-l",
}

// kubectl is deliberately absent from the safe list. Every verb of it,
// get and logs included, is a request to the cluster API server: it
// leaves the machine, carries the caller's credentials and can return
// secrets. The scan only models what a command does to the filesystem,
// so it has nothing to say about that, and a question it cannot answer
// belongs with the user.

// safeCommandUnsafeOptions void the read-only classification of an
// otherwise safe command, usually by making it run another program.
var safeCommandUnsafeOptions = map[string][]string{
	"rg":   {"--hostname-bin", "--pre", "--pre-glob"},
	"sort": {"--compress-program"},
	"tree": {"--output", "-o"},
}

// SafePrefix returns the number of leading words that identify a
// read-only command, or 0 when the words are not on the safe list. The
// count is also the narrowest scope a grant for this command may use.
func SafePrefix(words []string) int {
	if len(words) == 0 {
		return 0
	}
	switch head := commandHead(words); head {
	case "git":
		return gitSafePrefix(words)
	case "ps":
		if psDumpsEnvironment(words) {
			return 0
		}
		return 1
	default:
		if !slices.Contains(safeCommands, head) {
			return 0
		}
		unsafe := safeCommandUnsafeOptions[head]
		if slices.ContainsFunc(words[1:], func(w string) bool {
			return slices.Contains(unsafe, optionName(w))
		}) {
			return 0
		}
		return 1
	}
}

// isDangerous reports a dangerous verb anywhere in the words. It is
// applied per segment, so a chain is judged link by link.
func isDangerous(words []string) bool {
	if len(words) == 0 {
		return false
	}
	joined := strings.Join(words, " ")
	for _, pattern := range dangerousCommands {
		if matchesCommandPrefix(joined, pattern) {
			return true
		}
	}
	return false
}

func isExecVehicle(words []string) bool {
	head := commandHead(words)
	if head == "" {
		return false
	}
	if slices.Contains(execVehicles, head) {
		return true
	}
	// "find" only carries a command through -exec or -delete.
	if head == "find" {
		return slices.ContainsFunc(words[1:], func(w string) bool {
			return w == "-exec" || w == "-execdir" || w == "-delete" || w == "-ok"
		})
	}
	for _, family := range execVehicleFamilies {
		rest, ok := strings.CutPrefix(head, family)
		if !ok {
			continue
		}
		// A free-threaded build carries a trailing "t", as in python3.13t.
		rest = strings.TrimSuffix(rest, "t")
		if strings.IndexFunc(rest, func(r rune) bool {
			return (r < '0' || r > '9') && r != '.'
		}) < 0 {
			return true
		}
	}
	return false
}

// matchesCommandPrefix compares on word boundaries so that "git" does
// not match "gitleaks".
func matchesCommandPrefix(cmd, pattern string) bool {
	return cmd == pattern || strings.HasPrefix(cmd, pattern+" ")
}

func gitSafePrefix(words []string) int {
	i := 1
	for i < len(words) && strings.HasPrefix(words[i], "-") {
		opt, _, _ := strings.Cut(words[i], "=")
		if slices.Contains(gitUnsafeOptions, opt) {
			return 0
		}
		i++
	}
	if i >= len(words) {
		// A bare "git" only prints usage.
		return 1
	}

	verb, rest := words[i], words[i+1:]
	switch verb {
	case "config":
		if len(rest) == 0 || !slices.Contains(gitConfigReadOptions, optionName(rest[0])) {
			return 0
		}
	case "branch", "tag":
		if !gitRefQueryOnly(rest) {
			return 0
		}
	case "remote":
		// Subcommands other than show and get-url change remotes.
		for _, w := range rest {
			if strings.HasPrefix(w, "-") {
				continue
			}
			if w != "show" && w != "get-url" {
				return 0
			}
		}
	default:
		if !slices.Contains(gitReadOnlyVerbs, verb) {
			return 0
		}
		if slices.ContainsFunc(rest, func(w string) bool {
			return slices.Contains(gitUnsafeOptions, optionName(w))
		}) {
			return 0
		}
	}
	return i + 1
}

// psDumpsEnvironment reports the BSD-style "e" selector, which prints
// the environment of every process and so can leak secrets.
func psDumpsEnvironment(words []string) bool {
	return slices.ContainsFunc(words[1:], func(w string) bool {
		return !strings.HasPrefix(w, "-") && strings.Contains(w, "e")
	})
}

func optionName(w string) string {
	name, _, _ := strings.Cut(w, "=")
	return name
}

// gitRefQueryOnly reports that a branch or tag invocation only reads.
// Every word must be an option the query list explains, or the value
// belonging to one; a bare operand is a ref name being created.
func gitRefQueryOnly(rest []string) bool {
	for i := 0; i < len(rest); i++ {
		name, _, hasValue := strings.Cut(rest[i], "=")
		if !slices.Contains(gitRefQueryOptions, name) {
			return false
		}
		if !hasValue && slices.Contains(gitRefQueryValueOptions, name) {
			// The value cannot be a ref being written, so step over it.
			i++
		}
	}
	return true
}
