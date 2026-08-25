package permission

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/NaturalSelect/angela/internal/filepathext"
	"github.com/NaturalSelect/angela/internal/permission/shellscan"
	"github.com/bmatcuk/doublestar/v4"
	"github.com/invopop/jsonschema"
)

// RuleAction is what a matching rule does. Deny is the zero value so
// that a rule written without an action refuses rather than grants.
type RuleAction uint8

const (
	RuleDeny RuleAction = iota
	RuleAsk
	RuleAllow
)

var ruleActionNames = [...]string{
	RuleDeny:  "deny",
	RuleAsk:   "ask",
	RuleAllow: "allow",
}

func (a RuleAction) String() string {
	if int(a) >= len(ruleActionNames) {
		return "deny"
	}
	return ruleActionNames[a]
}

func ParseRuleAction(s string) (RuleAction, bool) {
	for i, name := range ruleActionNames {
		if name == s {
			return RuleAction(i), true
		}
	}
	return RuleDeny, false
}

func (a RuleAction) MarshalJSON() ([]byte, error) { return json.Marshal(a.String()) }

func (a *RuleAction) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	action, ok := ParseRuleAction(name)
	if !ok {
		return fmt.Errorf("unknown rule action %q", name)
	}
	*a = action
	return nil
}

// PatternMode selects how a rule's pattern is compared. Auto picks by
// access category, which is what almost every rule wants.
type PatternMode uint8

const (
	PatternAuto PatternMode = iota
	// PatternPath compares against a filesystem path, where "*" stops
	// at a separator and "**" crosses it.
	PatternPath
	// PatternFree compares against free text, where "*" crosses
	// anything.
	PatternFree
	// PatternDomain compares against the host of a URL, matching the
	// host itself and any subdomain of it.
	PatternDomain
)

var patternModeNames = [...]string{
	PatternAuto:   "auto",
	PatternPath:   "path",
	PatternFree:   "free",
	PatternDomain: "domain",
}

func (m PatternMode) String() string {
	if int(m) >= len(patternModeNames) {
		return "auto"
	}
	return patternModeNames[m]
}

func ParsePatternMode(s string) (PatternMode, bool) {
	for i, name := range patternModeNames {
		if name == s {
			return PatternMode(i), true
		}
	}
	return PatternAuto, false
}

func (m PatternMode) MarshalJSON() ([]byte, error) { return json.Marshal(m.String()) }

func (m *PatternMode) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	mode, ok := ParsePatternMode(name)
	if !ok {
		return fmt.Errorf("unknown pattern mode %q", name)
	}
	*m = mode
	return nil
}

// JSONSchema declares the wire form, which is the name rather than the
// underlying integer the constants carry.
func (RuleAction) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "string",
		Enum:        []any{"allow", "ask", "deny"},
		Description: "What to do when the rule matches",
	}
}

// JSONSchema declares the wire form, which is the name rather than the
// underlying integer the constants carry.
func (PatternMode) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:        "string",
		Enum:        []any{"auto", "path", "free", "domain"},
		Description: "How Pattern is compared: auto picks by action",
	}
}

// Rule is one declarative permission statement.
type Rule struct {
	// Action is what to do when the rule matches.
	Action RuleAction `json:"action"`
	// Tool narrows the rule. It matches either an access category
	// ("read", "edit", "execute", "network", "mcp", "list") or a single
	// tool name ("bash", "view"). Empty matches everything.
	Tool string `json:"tool,omitempty"`
	// Pattern narrows the rule further. Empty or "*" matches everything.
	Pattern string `json:"pattern,omitempty"`
	// Mode selects how Pattern is compared.
	Mode PatternMode `json:"mode,omitempty"`
}

// PromptPolicy decides what happens when no rule settles a request.
type PromptPolicy uint8

const (
	// PromptAsk shows the request to the user.
	PromptAsk PromptPolicy = iota
	// PromptDeny refuses without asking, for sessions that cannot
	// prompt.
	PromptDeny
	// PromptAllow grants without asking. It never overrides a deny
	// rule or a dangerous command.
	PromptAllow
)

var promptPolicyNames = [...]string{
	PromptAsk:   "ask",
	PromptDeny:  "deny",
	PromptAllow: "allow",
}

func (p PromptPolicy) String() string {
	if int(p) >= len(promptPolicyNames) {
		return "ask"
	}
	return promptPolicyNames[p]
}

func ParsePromptPolicy(s string) (PromptPolicy, bool) {
	for i, name := range promptPolicyNames {
		if name == s {
			return PromptPolicy(i), true
		}
	}
	return PromptAsk, false
}

// Verdict is the outcome of evaluating a policy against one access.
type Verdict struct {
	Action  RuleAction
	Reason  string
	Matched bool
}

// Policy is a compiled, immutable rule set.
type Policy struct {
	rules  []Rule
	prompt PromptPolicy
}

// builtinAllowedTools are tool names that ship pre-approved. They are
// ordinary allow rules rather than a hardcoded bypass, so a user's deny
// or ask rule still overrules them.
var builtinAllowedTools = []string{
	"mcp_docker_mcp-find",
	"mcp_docker_mcp-add",
	"mcp_docker_mcp-remove",
	"mcp_docker_mcp-config-set",
	"mcp_docker_code-mode",
}

// CompilePolicy validates rules and folds the legacy flat allow-list
// into them. Legacy entries are "tool" or "tool:action" strings and
// become unconditional allow rules, so a deny rule still overrules
// them.
func CompilePolicy(rules []Rule, legacyAllowedTools []string, prompt PromptPolicy) (*Policy, error) {
	compiled := make([]Rule, 0, len(rules)+len(legacyAllowedTools)+len(builtinAllowedTools))

	for i, rule := range rules {
		if rule.Pattern != "" && rule.Pattern != "*" && !doublestar.ValidatePattern(rule.Pattern) {
			return nil, fmt.Errorf("permission rule %d: invalid pattern %q", i, rule.Pattern)
		}
		compiled = append(compiled, rule)
	}

	for _, entry := range legacyAllowedTools {
		tool, _, _ := strings.Cut(entry, ":")
		if tool == "" {
			continue
		}
		compiled = append(compiled, Rule{Action: RuleAllow, Tool: tool})
	}

	for _, tool := range builtinAllowedTools {
		compiled = append(compiled, Rule{Action: RuleAllow, Tool: tool})
	}

	return &Policy{rules: compiled, prompt: prompt}, nil
}

// Prompt reports what to do when no rule settles a request.
func (p *Policy) Prompt() PromptPolicy {
	if p == nil {
		return PromptAsk
	}
	return p.prompt
}

// Evaluate walks the rules once. Deny short-circuits, ask outranks
// allow, and an unmatched access is left for the caller to settle.
//
// Some accesses have more than one consequence: a shell command runs
// every link in its chain, and a download both fetches a URL and writes
// a file. Those are judged leg by leg, because a rule written for one
// leg says nothing about the other.
func (p *Policy) Evaluate(access Access, cwd string) Verdict {
	if p == nil || len(p.rules) == 0 {
		return Verdict{}
	}
	switch {
	case access.Action == ActionExecute && access.Command != "":
		return p.evaluateCommand(access, cwd)
	case access.Action == ActionNetwork && access.Path != "":
		return p.evaluateLanding(access, cwd)
	default:
		return p.evaluateOne(access, cwd)
	}
}

// evaluateLanding judges a network access that lands on disk as the two
// things it is: a request to the URL and a write to the file. An allow
// must cover both, so a rule opening a domain cannot also hand out the
// filesystem.
func (p *Policy) evaluateLanding(access Access, cwd string) Verdict {
	legs := []Verdict{
		p.evaluateOne(access, cwd),
		p.evaluateOne(landingAccess(access), cwd),
	}

	if v, ok := firstWithAction(legs, RuleDeny); ok {
		return v
	}
	if v, ok := firstWithAction(legs, RuleAsk); ok {
		return v
	}
	for _, v := range legs {
		if !v.Matched || v.Action != RuleAllow {
			return Verdict{}
		}
	}
	return Verdict{
		Action:  RuleAllow,
		Reason:  "both the request and the file it writes match an allow rule",
		Matched: true,
	}
}

// landingAccess re-describes where a download puts its bytes as the
// plain write it is. The tool name is kept so a rule naming the tool
// still covers both legs.
func landingAccess(access Access) Access {
	landing := access
	landing.URL = ""
	landing.Action = ActionEdit
	return landing
}

// firstWithAction returns the first verdict that settled on the given
// action.
func firstWithAction(verdicts []Verdict, action RuleAction) (Verdict, bool) {
	for _, v := range verdicts {
		if v.Matched && v.Action == action {
			return v, true
		}
	}
	return Verdict{}, false
}

// evaluateCommand judges a shell command as written, again link by
// link, and again on the files each link touches. Deny or ask on any
// link settles the command, while an allow must cover every link: one
// uncovered link would carry whatever it likes past a rule written for
// its neighbour.
//
// Judging the file operands is what keeps a path rule honest. Without
// it `deny read **/.env` would stop the view tool and wave through
// `cat .env`, which is the same read by another route.
func (p *Policy) evaluateCommand(access Access, cwd string) Verdict {
	scan := shellscan.Scan(access.Command, cwd)

	verdicts := []Verdict{p.evaluateOne(access, cwd)}
	links := 0
	for _, segment := range scan.Segments {
		link := access
		link.Command = strings.Join(segment.Words, " ")
		verdicts = append(verdicts, p.evaluateOne(link, cwd))
		links++
	}

	// File verdicts can refuse but never approve: a rule allowing a
	// path says nothing about the command that reaches it.
	settling := verdicts
	for _, segment := range scan.Segments {
		for _, ref := range segment.Files {
			settling = append(settling, p.evaluateOne(fileAccess(access, ref), cwd))
		}
	}

	if v, ok := firstWithAction(settling, RuleDeny); ok {
		return v
	}
	if v, ok := firstWithAction(settling, RuleAsk); ok {
		return v
	}
	if scan.Opaque || links == 0 {
		return Verdict{}
	}
	for _, v := range verdicts[1:] {
		if !v.Matched || v.Action != RuleAllow {
			return Verdict{}
		}
	}
	return Verdict{
		Action:  RuleAllow,
		Reason:  "every command in the chain matches an allow rule",
		Matched: true,
	}
}

// fileAccess re-describes a file a command touches as the plain read or
// write it is, so the same rules cover both routes to it.
func fileAccess(access Access, ref shellscan.FileRef) Access {
	file := access
	file.Command = ""
	file.Path = ref.Path
	file.Action = ActionRead
	if ref.Write {
		file.Action = ActionEdit
	}
	return file
}

func (p *Policy) evaluateOne(access Access, cwd string) Verdict {
	var ask Verdict
	var allow Verdict

	// Resolved once per access rather than once per rule: building the
	// forms walks symlinks, and every rule would get the same answer.
	var forms []string
	switch access.Action {
	case ActionRead, ActionList, ActionEdit:
		forms = pathMatchForms(access.Path, cwd)
	}

	for _, rule := range p.rules {
		if !ruleMatches(rule, access, forms) {
			continue
		}
		reason := ruleReason(rule, access)
		switch rule.Action {
		case RuleDeny:
			return Verdict{Action: RuleDeny, Reason: reason, Matched: true}
		case RuleAsk:
			if !ask.Matched {
				ask = Verdict{Action: RuleAsk, Reason: reason, Matched: true}
			}
		case RuleAllow:
			if !allow.Matched {
				allow = Verdict{Action: RuleAllow, Reason: reason, Matched: true}
			}
		}
	}

	if ask.Matched {
		return ask
	}
	return allow
}

func ruleMatches(rule Rule, access Access, pathForms []string) bool {
	if !toolFilterMatches(rule.Tool, access) {
		return false
	}
	if rule.Pattern == "" || rule.Pattern == "*" {
		return true
	}
	return patternMatches(rule, access, pathForms)
}

// toolFilterMatches accepts either an access category or a concrete
// tool name, so "execute" covers every command runner while "bash"
// singles one out.
func toolFilterMatches(filter string, access Access) bool {
	if filter == "" || filter == "*" {
		return true
	}
	if filter == access.Tool {
		return true
	}
	action, ok := ParseAction(filter)
	return ok && action == access.Action
}

func patternMatches(rule Rule, access Access, pathForms []string) bool {
	mode := rule.Mode
	if mode == PatternAuto {
		mode = defaultPatternMode(access.Action)
	}

	switch access.Action {
	case ActionRead, ActionList, ActionEdit:
		for _, form := range pathForms {
			if matchText(rule.Pattern, form, mode) {
				return true
			}
		}
		return false
	case ActionExecute:
		return matchText(rule.Pattern, access.Command, mode)
	case ActionNetwork:
		if mode == PatternDomain {
			return domainMatches(rule.Pattern, access.URL)
		}
		return matchText(rule.Pattern, access.URL, mode)
	case ActionMCP:
		qualified := access.Server
		if access.MCPTool != "" {
			qualified += "/" + access.MCPTool
		}
		return matchText(rule.Pattern, qualified, mode) ||
			matchText(rule.Pattern, access.Tool, mode)
	}
	return false
}

func defaultPatternMode(action Action) PatternMode {
	switch action {
	case ActionRead, ActionList, ActionEdit:
		return PatternPath
	default:
		return PatternFree
	}
}

func matchText(pattern, text string, mode PatternMode) bool {
	if text == "" {
		return false
	}
	if mode == PatternPath {
		ok, err := doublestar.Match(pattern, text)
		return err == nil && ok
	}
	return freeMatch(pattern, text)
}

// pathMatchForms lists the spellings a path rule may be written
// against, so that "**/.env" catches ".env", "./.env" and the absolute
// path alike.
//
// The symlink-resolved path is one of them. Scope checks resolve links
// before judging a path, and a rule naming a real location has to see
// the same thing: with `escape -> /etc` in the workspace, matching only
// the lexical spelling would let `escape/passwd` walk past
// `deny read /etc/**`.
func pathMatchForms(path, cwd string) []string {
	if path == "" {
		return nil
	}
	forms := []string{filepath.ToSlash(path)}
	if strings.HasPrefix(path, "~") {
		return forms
	}

	abs := path
	if !filepathext.SmartIsAbs(abs) && cwd != "" {
		abs = filepath.Join(cwd, abs)
	}
	abs = filepath.ToSlash(filepath.Clean(abs))
	forms = appendForm(forms, abs)
	forms = appendForm(forms, filepath.ToSlash(resolvePath(path, cwd)))

	if cwd != "" {
		if rel, err := filepath.Rel(cwd, filepath.FromSlash(abs)); err == nil &&
			rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			rel = filepath.ToSlash(rel)
			forms = appendForm(forms, rel)
			forms = appendForm(forms, "./"+rel)
		}
	}
	return forms
}

// appendForm adds a spelling that is not already listed.
func appendForm(forms []string, form string) []string {
	if form == "" || slices.Contains(forms, form) {
		return forms
	}
	return append(forms, form)
}

// domainMatches compares a host pattern against the host of a URL,
// covering the host itself and any subdomain beneath it.
//
// The host comes from a real parse rather than from cutting the string
// apart. Userinfo may carry both a colon and an @, so
// "https://docs.example.com:pass@evil.example/path" hands a hand-rolled
// splitter the allowed host while the request goes to evil.example.
//
// Anything that is not an http(s) URL with a host matches nothing: a
// search query arrives here too, and no scheme reaches the network,
// since every tool that fetches rejects one it cannot request.
func domainMatches(pattern, rawURL string) bool {
	pattern = strings.ToLower(strings.TrimPrefix(pattern, "."))
	if pattern == "" {
		return false
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}

	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" {
		return false
	}
	return host == pattern || strings.HasSuffix(host, "."+pattern)
}

func ruleReason(rule Rule, access Access) string {
	subject := rule.Tool
	if subject == "" {
		subject = access.Action.String()
	}
	if rule.Pattern == "" || rule.Pattern == "*" {
		return fmt.Sprintf("permission policy: %s rule on %s", rule.Action, subject)
	}
	return fmt.Sprintf("permission policy: %s rule on %s matching %q",
		rule.Action, subject, rule.Pattern)
}
