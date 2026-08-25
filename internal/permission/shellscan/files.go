package shellscan

import (
	"path/filepath"
	"slices"
	"strings"
)

// readCommands take their positional operands as files to read.
var readCommands = []string{
	"ack", "ag", "awk", "base32", "base64", "bat", "bzcat", "bzgrep",
	"cat", "cd", "comm", "cut", "diff", "du", "egrep", "less", "fgrep",
	"file", "grep", "head", "hexdump", "jq", "ls", "more", "nl", "od",
	"rev", "rg", "sed", "sort", "stat", "strings", "tac", "tail", "tree",
	"wc", "xxd", "xzcat", "xzgrep", "yq", "zcat", "zgrep", "zless",
	"zmore", "zstdcat",
}

// writeCommands take their positional operands as files to write.
var writeCommands = []string{"tee", "truncate"}

// patternFirstCommands take a search pattern as their first positional
// operand rather than a path.
var patternFirstCommands = []string{
	"ack", "ag", "awk", "egrep", "fgrep", "grep", "rg", "sed",
}

// patternOptions supply the pattern through an option, which makes the
// first positional operand a file rather than the pattern.
var patternOptions = []string{"--file", "--regexp", "-e", "-f"}

// fileOptionCommands maps a command to the options whose value is a
// path it reads. Such a path is invisible to the positional scan: the
// option word is skipped and its value is either skipped with it or
// mistaken for an operand, so without this table a command can open a
// file that no rule ever sees.
var fileOptionCommands = map[string][]string{
	"du":    {"--files0-from"},
	"egrep": {"--file", "-f"},
	"fgrep": {"--file", "-f"},
	"git":   {"--file", "-f"},
	"grep":  {"--file", "-f"},
	"rg":    {"--file", "-f"},
	"sort":  {"--files0-from"},
	"wc":    {"--files0-from"},
}

// outputOptionCommands write to the path given by -o or --output.
var outputOptionCommands = []string{"go", "rustc", "sort"}

// commandFiles returns the paths a segment touches and reports whether
// it walks a tree instead of named operands.
func (s *scanner) commandFiles(words []string) ([]FileRef, bool) {
	head := commandHead(words)
	candidates := fileCandidates(words)
	if slices.Contains(patternFirstCommands, head) && len(candidates) > 0 &&
		!hasOption(words, patternOptions) {
		candidates = candidates[1:]
	}

	var refs []FileRef
	add := func(path string, write bool) {
		anchored, _ := globAnchor(path)
		refs = append(refs, FileRef{Path: joinPath(s.cwd, anchored), Write: write})
	}

	// Files named by an option are read whatever else the command does,
	// so they are collected before the per-command operand rules.
	optionFiles, unpinned := optionFiles(head, words)
	for _, path := range optionFiles {
		add(path, false)
	}

	switch head {
	case "dd":
		for _, w := range words[1:] {
			if path, ok := strings.CutPrefix(w, "if="); ok {
				add(path, false)
			}
			if path, ok := strings.CutPrefix(w, "of="); ok {
				add(path, true)
			}
		}
		return refs, unpinned

	case "cp", "mv", "ln", "install":
		if len(candidates) < 2 {
			// Without a distinguishable destination the operands cannot
			// be classified.
			return nil, true
		}
		for _, src := range candidates[:len(candidates)-1] {
			add(src, false)
		}
		add(candidates[len(candidates)-1], true)
		return refs, unpinned

	case "rm", "rmdir", "mkdir", "touch", "rustfmt":
		for _, c := range candidates {
			add(c, true)
		}
		return refs, unpinned

	case "uniq":
		// uniq [INPUT [OUTPUT]]
		for i, c := range candidates {
			add(c, i == 1)
		}
		return refs, unpinned
	}

	if slices.Contains(outputOptionCommands, head) {
		for _, path := range optionValues(words, "-o", "--output") {
			add(path, true)
		}
	}

	write := slices.Contains(writeCommands, head)
	if !write && !slices.Contains(readCommands, head) {
		return refs, unpinned
	}

	recursive := readerRecurses(head, words, candidates) || unpinned
	if len(candidates) == 0 && len(optionFiles) == 0 {
		// A reader with no operand walks the working directory.
		add(".", write)
		return refs, recursive
	}
	for _, c := range candidates {
		add(c, write)
	}
	return refs, recursive
}

// optionFiles returns the paths a command reads through its file-bearing
// options, and reports whether one of them could not be read out of the
// words. An option the scan cannot parse is not one it may ignore: the
// command opens the file either way, so the segment stops being
// pinnable to its operands.
func optionFiles(head string, words []string) ([]string, bool) {
	names, ok := fileOptionCommands[head]
	if !ok {
		return nil, false
	}
	values := optionValues(words, names...)
	return values, len(values) != countOption(words, names)
}

// countOption counts the occurrences of any of names, including a short
// option that carries its value with no separator such as "-fpat.txt".
func countOption(words, names []string) int {
	n := 0
	for _, w := range words[1:] {
		if slices.Contains(names, optionName(w)) || attachedShortOption(w, names) {
			n++
		}
	}
	return n
}

// attachedShortOption reports a short option whose value is glued to it,
// as in "-fpat.txt". The value cannot be split off without knowing the
// command, so the caller treats it as unparsed rather than guessing.
func attachedShortOption(w string, names []string) bool {
	if len(w) <= 2 || !strings.HasPrefix(w, "-") || strings.HasPrefix(w, "--") {
		return false
	}
	return slices.Contains(names, w[:2])
}

// hasOption reports any of names among the command's option words.
func hasOption(words, names []string) bool {
	return slices.ContainsFunc(words[1:], func(w string) bool {
		return slices.Contains(names, optionName(w)) || attachedShortOption(w, names)
	})
}

// readerRecurses reports that the segment walks a directory tree, so
// the files it reads cannot be pinned to its operands.
func readerRecurses(head string, words, candidates []string) bool {
	switch head {
	case "grep", "egrep", "fgrep":
		return slices.ContainsFunc(words[1:], func(w string) bool {
			if w == "--recursive" || w == "--dereference-recursive" {
				return true
			}
			return strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--") &&
				strings.ContainsAny(w, "rR")
		})
	case "ack", "ag", "rg", "tree":
		return len(candidates) == 0
	}
	return false
}

// fileCandidates returns the positional operands, honouring the "--"
// end-of-options marker so that a path such as "-/../.env" is seen.
func fileCandidates(words []string) []string {
	var out []string
	positional := false
	for _, w := range words[1:] {
		if !positional {
			if w == "--" {
				positional = true
				continue
			}
			if strings.HasPrefix(w, "-") && w != "-" {
				continue
			}
		}
		out = append(out, w)
	}
	return out
}

func optionValues(words []string, names ...string) []string {
	var out []string
	for i, w := range words {
		name, value, hasValue := strings.Cut(w, "=")
		if !slices.Contains(names, name) {
			continue
		}
		if hasValue {
			out = append(out, value)
			continue
		}
		if i+1 < len(words) {
			out = append(out, words[i+1])
		}
	}
	return out
}

// globAnchor returns the deepest directory a pattern can expand within,
// so that a wildcard operand still pins to a location.
func globAnchor(path string) (string, bool) {
	i := strings.IndexAny(path, "*?[{")
	if i < 0 {
		return path, false
	}
	cut := strings.LastIndex(path[:i], "/")
	switch cut {
	case -1:
		return ".", true
	case 0:
		return "/", true
	default:
		return path[:cut], true
	}
}

// joinPath anchors a relative operand to dir without folding "." or
// ".." components, so that callers can resolve symlinks first.
func joinPath(dir, path string) string {
	switch {
	case path == "":
		return dir
	case dir == "":
		return path
	case filepath.IsAbs(path):
		return path
	case strings.HasPrefix(path, "~"):
		// A home-relative path has its own anchor.
		return path
	case strings.HasPrefix(path, "/"):
		return path
	}
	return strings.TrimSuffix(dir, "/") + "/" + path
}

// withinDir reports whether path resolves inside dir. Symlinks are not
// followed here; callers that need that must resolve them first.
func withinDir(dir, path string) bool {
	if dir == "" {
		return false
	}
	if strings.HasPrefix(path, "~") {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
