// Package shellscan performs static analysis of shell commands for the
// permission system. It splits a command into segments, reports the
// files each segment touches, and flags segments that cannot be judged
// safe on their own.
//
// Analysis is whitelist based: any construct the scanner does not
// explicitly model marks the result opaque, and callers must treat that
// as fail-closed. A blacklist of dangerous metacharacters can never be
// completed, whereas an unmodelled syntax node fails by itself.
package shellscan

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// FileRef is a filesystem path a segment touches.
type FileRef struct {
	// Path is the operand anchored to the segment's working directory.
	// It is not symlink-resolved and may still contain "." or ".."
	// components, so that callers can resolve links before folding.
	Path string
	// Write reports that the segment may modify the path.
	Write bool
}

// Segment is a single command in a chain, after wrapper stripping.
type Segment struct {
	// Words are the command words with wrappers such as timeout or env
	// peeled off.
	Words []string
	// Raw is the original source text of the segment.
	Raw string
	// Files are the paths the segment reads or writes.
	Files []FileRef
	// Dangerous reports a verb from the dangerous list. Such segments
	// must never be satisfied by a stored grant.
	Dangerous bool
	// Vehicle reports that the head can carry an arbitrary command, so
	// the words do not describe what actually runs.
	Vehicle bool
	// Recursive reports that the segment walks a tree rather than named
	// operands, so the exact set of files cannot be pinned.
	Recursive bool
}

// Result is the outcome of scanning one command string.
type Result struct {
	Segments []Segment
	// Opaque reports that analysis could not decompose the command.
	// Callers must prompt, never allow.
	Opaque bool
	// Reason explains an opaque result, for display to the user.
	Reason string
}

// Scan decomposes command into segments, resolving relative operands
// against cwd. A command it cannot fully model yields an opaque result.
func Scan(command, cwd string) Result {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).
		Parse(strings.NewReader(command), "")
	if err != nil {
		return Result{Opaque: true, Reason: "command could not be parsed"}
	}
	s := &scanner{src: command, cwd: cwd}
	for _, stmt := range file.Stmts {
		s.walkStmt(stmt)
	}
	if s.opaque {
		return Result{Opaque: true, Reason: s.reason}
	}
	return Result{Segments: s.segments}
}

// Safe reports whether every segment can be judged safe to run without
// asking the user. An opaque result is never safe.
//
// contains decides whether a path the command touches counts as inside
// the area the caller allows. It is injected because answering that
// honestly means resolving symlinks, and this package stays a pure
// analysis layer that never touches the filesystem.
func (r Result) Safe(contains func(path string) bool) bool {
	if r.Opaque || len(r.Segments) == 0 {
		return false
	}
	for _, seg := range r.Segments {
		if seg.Dangerous || seg.Vehicle || seg.Recursive {
			return false
		}
		if SafePrefix(seg.Words) == 0 {
			return false
		}
		for _, f := range seg.Files {
			if f.Write || !contains(f.Path) {
				return false
			}
		}
	}
	return true
}

type scanner struct {
	src      string
	cwd      string
	segments []Segment
	opaque   bool
	reason   string
}

// fail marks the scan opaque. The first reason wins, since later nodes
// are analysed on assumptions the failure already invalidated.
func (s *scanner) fail(reason string) {
	if !s.opaque {
		s.opaque = true
		s.reason = reason
	}
}

func (s *scanner) walkStmt(st *syntax.Stmt) {
	if s.opaque {
		return
	}
	switch {
	case st.Background:
		s.fail("background execution is not analyzable")
		return
	case st.Coprocess:
		s.fail("coprocesses are not analyzable")
		return
	case st.Disown:
		s.fail("disowned jobs are not analyzable")
		return
	case st.Negated:
		s.fail("negated commands are not analyzable")
		return
	}

	first := len(s.segments)
	s.walkCmd(st.Cmd)
	if s.opaque {
		return
	}

	files := s.redirFiles(st.Redirs)
	if s.opaque {
		return
	}
	if len(s.segments) == first {
		if len(files) > 0 {
			s.segments = append(s.segments, Segment{
				Raw:   s.text(st.Pos().Offset(), st.End().Offset()),
				Files: files,
			})
		}
		return
	}
	for i := first; i < len(s.segments); i++ {
		s.segments[i].Files = append(s.segments[i].Files, files...)
	}
}

func (s *scanner) walkCmd(cmd syntax.Command) {
	switch c := cmd.(type) {
	case nil:
		// A statement carrying only redirections; the caller handles it.
	case *syntax.CallExpr:
		s.callSegment(c)
	case *syntax.BinaryCmd:
		switch c.Op {
		case syntax.AndStmt, syntax.OrStmt, syntax.Pipe:
			s.walkStmt(c.X)
			s.walkStmt(c.Y)
		default:
			s.fail("operator " + c.Op.String() + " is not analyzable")
		}
	default:
		s.fail(commandKind(cmd) + " is not analyzable")
	}
}

func (s *scanner) callSegment(c *syntax.CallExpr) {
	// A prefix assignment can hijack PATH or LD_PRELOAD, and it is
	// invisible in the command words that a stored grant is keyed on.
	if len(c.Assigns) > 0 {
		s.fail("environment assignment is not analyzable")
		return
	}
	if len(c.Args) == 0 {
		return
	}

	words := make([]string, 0, len(c.Args))
	for _, arg := range c.Args {
		lit, ok := wordLiteral(arg)
		if !ok {
			s.fail("expansions and substitutions are not analyzable")
			return
		}
		words = append(words, lit)
	}

	norm, ok := normalizeWords(words)
	if !ok {
		s.fail("command wrapper could not be resolved")
		return
	}
	if len(norm) == 0 {
		return
	}

	seg := Segment{
		Words: norm,
		Raw:   s.text(c.Pos().Offset(), c.End().Offset()),
	}
	seg.Dangerous = isDangerous(norm)
	seg.Vehicle = isExecVehicle(norm)
	seg.Files, seg.Recursive = s.commandFiles(norm)
	s.segments = append(s.segments, seg)

	s.trackChdir(norm)
}

// trackChdir follows cd so that later segments resolve their relative
// operands against the directory they actually run in.
func (s *scanner) trackChdir(words []string) {
	if commandHead(words) != "cd" {
		return
	}
	args := fileCandidates(words)
	if len(args) != 1 {
		s.fail("cd without a single literal target is not analyzable")
		return
	}
	s.cwd = joinPath(s.cwd, args[0])
}

func (s *scanner) redirFiles(redirs []*syntax.Redirect) []FileRef {
	var out []FileRef
	for _, r := range redirs {
		switch r.Op {
		case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
			// Here-document bodies are stdin data, not files.
			continue
		}
		if r.Word == nil {
			continue
		}
		target, ok := wordLiteral(r.Word)
		if !ok {
			s.fail("redirection target is not analyzable")
			return nil
		}

		write := true
		switch r.Op {
		case syntax.RdrIn:
			write = false
		case syntax.DplIn:
			// "<&2" duplicates a descriptor; "<&file" reads a file.
			if isDescriptorRef(target) {
				continue
			}
			write = false
		case syntax.DplOut:
			// ">&2" duplicates a descriptor; ">&file" writes a file.
			if isDescriptorRef(target) {
				continue
			}
		}
		if write && isSafeWriteSink(target) {
			continue
		}
		out = append(out, FileRef{Path: joinPath(s.cwd, target), Write: write})
	}
	return out
}

func (s *scanner) text(start, end uint) string {
	if end > uint(len(s.src)) || start >= end {
		return ""
	}
	return s.src[start:end]
}

// wordLiteral renders a word that expands to itself. Anything the shell
// would rewrite at run time - parameters, substitutions, process
// substitutions, extended globs - has no literal form and fails.
func wordLiteral(w *syntax.Word) (string, bool) {
	var sb strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			sb.WriteString(p.Value)
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", false
			}
			sb.WriteString(p.Value)
		case *syntax.DblQuoted:
			if p.Dollar {
				return "", false
			}
			for _, inner := range p.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return "", false
				}
				sb.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return sb.String(), true
}

func commandKind(cmd syntax.Command) string {
	switch cmd.(type) {
	case *syntax.Subshell:
		return "subshell"
	case *syntax.Block:
		return "command block"
	case *syntax.IfClause:
		return "conditional"
	case *syntax.WhileClause, *syntax.ForClause:
		return "loop"
	case *syntax.CaseClause:
		return "case clause"
	case *syntax.FuncDecl:
		return "function declaration"
	case *syntax.ArithmCmd:
		return "arithmetic command"
	case *syntax.TestClause:
		return "test clause"
	case *syntax.DeclClause:
		return "declaration"
	case *syntax.LetClause:
		return "let clause"
	case *syntax.TimeClause:
		return "time clause"
	case *syntax.CoprocClause:
		return "coprocess clause"
	case *syntax.TestDecl:
		return "test declaration"
	}
	return "shell construct"
}

func isDescriptorRef(s string) bool {
	if s == "-" {
		return true
	}
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isSafeWriteSink(path string) bool {
	switch path {
	case "/dev/null", "/dev/stdout", "/dev/stderr":
		return true
	}
	return false
}
