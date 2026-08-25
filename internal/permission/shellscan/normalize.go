package shellscan

import "strings"

// maxNormalizeRounds bounds the alternation between wrapper peeling and
// transparent prefix peeling.
const maxNormalizeRounds = 16

// wrapperOptionArgs lists, per wrapper, the options that consume a
// separate following token. Anything else starting with "-" is assumed
// to be a flag that stands alone.
var wrapperOptionArgs = map[string][]string{
	"timeout": {"-k", "-s", "--kill-after", "--signal"},
	"nice":    {"-n", "--adjustment"},
	"ionice": {
		"-c", "-n", "-p", "-P", "-u",
		"--class", "--classdata", "--pid", "--pgid", "--uid",
	},
	"stdbuf": {"-i", "-o", "-e"},
}

// wrapperOperands lists wrappers that take a mandatory positional
// operand of their own before the command begins.
var wrapperOperands = map[string]int{
	"timeout": 1, // DURATION
	"chrt":    1, // PRIORITY
}

// transparentPrefixes run the rest of the words as a command without
// changing what it does.
var transparentPrefixes = []string{"exec", "command", "builtin"}

// normalizeWords peels wrappers such as timeout, nice and env until the
// words describe the command that actually runs. It reports false when
// the peel is ambiguous, so callers fail closed rather than judging a
// wrapper as if it were the command.
func normalizeWords(words []string) ([]string, bool) {
	cur := words
	for range maxNormalizeRounds {
		next, ok, changed := stripWrapper(cur)
		if !ok {
			return nil, false
		}
		if changed {
			cur = next
			continue
		}
		next, ok, changed = peelTransparent(cur)
		if !ok {
			return nil, false
		}
		if !changed {
			return cur, true
		}
		cur = next
	}
	return nil, false
}

// stripWrapper removes one leading wrapper command. It reports changed
// when it peeled something, and ok=false when the wrapper's own options
// could not be resolved.
func stripWrapper(words []string) (out []string, ok, changed bool) {
	head := commandHead(words)
	if head == "env" {
		return stripEnv(words)
	}

	optArgs, isWrapper := wrapperOptionArgs[head]
	if !isWrapper && head != "nohup" && head != "chrt" {
		return words, true, false
	}

	i := 1
	for i < len(words) && strings.HasPrefix(words[i], "-") {
		opt, _, hasValue := strings.Cut(words[i], "=")
		if !hasValue && containsString(optArgs, opt) {
			i += 2
			continue
		}
		i++
	}
	i += wrapperOperands[head]
	if i >= len(words) {
		return nil, false, false
	}
	return words[i:], true, true
}

// stripEnv peels "env" only when it neither rewrites argv nor sets a
// variable. A prefix assignment can hijack PATH or LD_PRELOAD and is
// invisible in the peeled words, so it fails closed.
func stripEnv(words []string) (out []string, ok, changed bool) {
	i := 1
	for i < len(words) {
		w := words[i]
		switch {
		case w == "--":
			i++
			if i >= len(words) {
				return nil, false, false
			}
			return words[i:], true, true
		case w == "-i" || w == "--ignore-environment" || w == "-0" || w == "--null":
			i++
		case w == "-u" || w == "-C":
			i += 2
		case strings.HasPrefix(w, "--unset=") || strings.HasPrefix(w, "--chdir="):
			i++
		case w == "-S" || strings.HasPrefix(w, "--split-string"):
			// -S rewrites argv from a single string.
			return nil, false, false
		case strings.HasPrefix(w, "-"):
			// An unknown option may or may not consume the next token.
			return nil, false, false
		case strings.Contains(w, "="):
			// env NAME=VALUE cmd sets the environment for cmd.
			return nil, false, false
		default:
			return words[i:], true, true
		}
	}
	return nil, false, false
}

func peelTransparent(words []string) (out []string, ok, changed bool) {
	head := commandHead(words)
	if !containsString(transparentPrefixes, head) {
		return words, true, false
	}
	if len(words) < 2 {
		return nil, false, false
	}
	// "exec -a name cmd" and "command -v name" change what the words
	// mean, so a flag here is not a transparent peel.
	if strings.HasPrefix(words[1], "-") {
		return nil, false, false
	}
	return words[1:], true, true
}

// commandHead returns the lowercased base name of the command word.
func commandHead(words []string) string {
	if len(words) == 0 {
		return ""
	}
	head := words[0]
	if i := strings.LastIndexAny(head, `/\`); i >= 0 {
		head = head[i+1:]
	}
	return strings.TrimSuffix(strings.ToLower(head), ".exe")
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
