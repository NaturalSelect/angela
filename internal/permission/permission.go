package permission

import (
	"context"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/permission/shellscan"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/google/uuid"
)

// hookApprovalKey is the unexported context key used to mark a tool call as
// pre-approved by a PreToolUse hook. The value is the tool call ID so an
// approval can't be reused across calls that happen to share a context.
type hookApprovalKey struct{}

// WithHookApproval returns a context that marks the given tool call ID as
// pre-approved by a hook. When the permission service sees a matching
// request it short-circuits the normal prompt and grants immediately.
func WithHookApproval(ctx context.Context, toolCallID string) context.Context {
	return context.WithValue(ctx, hookApprovalKey{}, toolCallID)
}

// hookApproved reports whether the context carries a hook approval for the
// given tool call ID.
func hookApproved(ctx context.Context, toolCallID string) bool {
	if toolCallID == "" {
		return false
	}
	v, _ := ctx.Value(hookApprovalKey{}).(string)
	return v == toolCallID
}

// Outcome is how a permission request ended.
type Outcome uint8

const (
	// OutcomeAllow lets the call proceed.
	OutcomeAllow Outcome = iota
	// OutcomePolicyDeny refuses on the configuration's behalf. The
	// reason goes back to the model so it can take another route.
	OutcomePolicyDeny
	// OutcomeUserDeny refuses on the user's behalf, which ends the turn.
	OutcomeUserDeny
	// OutcomeCancelled reports that the caller's context ended first.
	OutcomeCancelled
)

// Decision is the result of gating one access.
type Decision struct {
	Outcome Outcome
	Reason  string
}

func (d Decision) Allowed() bool { return d.Outcome == OutcomeAllow }

// GateRequest asks the service to settle one access.
type GateRequest struct {
	SessionID  string
	ToolCallID string
	Access     Access
	// Preview is what the user sees if the request reaches the prompt.
	Preview Preview
}

// Preview is what the user is shown when a request reaches the prompt.
type Preview struct {
	Description string
	Params      any
}

// Ticket carries an access the gate has already evaluated, from the
// decision ladder to the prompt it ends at.
type Ticket struct {
	sessionID  string
	toolCallID string
	access     Access
	grant      GrantKey
	// forced marks a request no stored grant may satisfy, because the
	// command is dangerous or could not be analysed.
	forced bool
	reason string
}

// GrantKey scopes a session grant. Deriving it from the access rather
// than the tool means one approval covers exactly what the user saw.
type GrantKey struct {
	SessionID string
	Action    Action
	// Dir is where a command was approved to run. It is a field of its
	// own rather than part of Scope because a directory name may hold
	// the same characters a command does, and one joined string would
	// let a crafted pair land on a key the user approved for another.
	// Empty for everything but ActionExecute, whose Scope is the
	// command alone and means nothing without it.
	Dir   string
	Scope string
}

type CreatePermissionRequest struct {
	SessionID   string `json:"session_id"`
	ToolCallID  string `json:"tool_call_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Params      any    `json:"params"`
	Path        string `json:"path"`
}

type PermissionNotification struct {
	ToolCallID string `json:"tool_call_id"`
	Granted    bool   `json:"granted"`
	Denied     bool   `json:"denied"`
}

type PermissionRequest struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	ToolCallID  string `json:"tool_call_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Params      any    `json:"params"`
	Path        string `json:"path"`
}

type Service interface {
	pubsub.Subscriber[PermissionRequest]
	// GrantPersistent grants a permission request and remembers the grant
	// for the session. It returns true if this call actually resolved the
	// pending request; false if the request had already been resolved
	// (e.g., by another concurrent caller) or is unknown.
	GrantPersistent(permission PermissionRequest) bool
	// Grant grants a permission request. It returns true if this call
	// actually resolved the pending request; false if the request had
	// already been resolved or is unknown.
	Grant(permission PermissionRequest) bool
	// Deny denies a permission request. It returns true if this call
	// actually resolved the pending request; false if the request had
	// already been resolved or is unknown.
	Deny(permission PermissionRequest) bool
	// Gate settles one access. Unless the request asks to defer, it
	// prompts when it must and blocks until the user answers.
	Gate(ctx context.Context, req GateRequest) Decision
	// PolicyDenial reports the deny rule that refuses this access, if
	// any. A caller that must do work before it can describe a request
	// asks first, so a refused path is never read in order to build a
	// preview of reading it.
	PolicyDenial(access Access) (Decision, bool)
	// SetSessionPromptPolicy decides what a session does when a request
	// reaches the prompt. It can never override a deny rule or a
	// dangerous command.
	SetSessionPromptPolicy(sessionID string, policy PromptPolicy)
	// SetSessionUnattended records whether anything is attached to this
	// session that could answer a prompt. An unattended session refuses
	// a request that must be asked about instead of blocking on a
	// prompt nobody will ever see.
	SetSessionUnattended(sessionID string, unattended bool)
	// SessionUnattended reports what SetSessionUnattended recorded, so a
	// session spawned by another can inherit its answer.
	SessionUnattended(sessionID string) bool
	SetSkipRequests(skip bool)
	SkipRequests() bool
	SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[PermissionNotification]
}

type permissionService struct {
	*pubsub.Broker[PermissionRequest]

	notificationBroker *pubsub.Broker[PermissionNotification]
	workingDir         string
	// readRoots are the directories reading is free in. The working
	// directory is always one; skill directories are added because the
	// agent is expected to read the skills the user installed.
	readRoots          []string
	policy             *Policy
	sessionPermissions *csync.Map[GrantKey, bool]
	pendingRequests    *csync.Map[string, chan bool]
	pendingGrants      *csync.Map[string, Ticket]
	sessionPrompts     *csync.Map[string, PromptPolicy]
	// sessionUnattended marks sessions with no one to answer a prompt.
	sessionUnattended *csync.Map[string, bool]
	// sessionGates serialises prompts per session, so one session
	// waiting on the user never blocks another.
	sessionGates *csync.Map[string, chan struct{}]
	skip         atomic.Bool
}

// NewPermissionService builds the service. A nil policy settles nothing
// on its own, leaving every request to the prompt. Reads are free
// inside workingDir and any extra readRoots.
func NewPermissionService(workingDir string, skip bool, policy *Policy, readRoots ...string) Service {
	// Roots are resolved once here so every later comparison is
	// link-to-link rather than link-to-name.
	workingDir = resolvePath(workingDir, "")
	roots := make([]string, 0, len(readRoots)+1)
	roots = append(roots, workingDir)
	for _, root := range readRoots {
		if root != "" {
			roots = append(roots, resolvePath(root, workingDir))
		}
	}
	svc := &permissionService{
		Broker:             pubsub.NewBroker[PermissionRequest](),
		notificationBroker: pubsub.NewBroker[PermissionNotification](),
		workingDir:         workingDir,
		readRoots:          roots,
		policy:             policy,
		sessionPermissions: csync.NewMap[GrantKey, bool](),
		pendingRequests:    csync.NewMap[string, chan bool](),
		pendingGrants:      csync.NewMap[string, Ticket](),
		sessionPrompts:     csync.NewMap[string, PromptPolicy](),
		sessionUnattended:  csync.NewMap[string, bool](),
		sessionGates:       csync.NewMap[string, chan struct{}](),
	}
	svc.skip.Store(skip)
	return svc
}

// Gate walks the decision ladder. The order is load bearing: a deny
// rule is the configuration's word and outranks even the user's own
// skip switch, while a dangerous or unanalysable command outranks every
// form of pre-approval below it.
func (s *permissionService) Gate(ctx context.Context, req GateRequest) Decision {
	access := req.Access

	verdict := s.policy.Evaluate(access, s.workingDir)
	if decision, denied := denialOf(verdict); denied {
		return decision
	}

	if s.skip.Load() {
		return Decision{Outcome: OutcomeAllow, Reason: "permission prompts are disabled"}
	}

	forced, forcedReason := s.forcedPrompt(access)

	if !forced && hookApproved(ctx, req.ToolCallID) {
		s.notify(req.ToolCallID, true, false)
		return Decision{Outcome: OutcomeAllow, Reason: "approved by a PreToolUse hook"}
	}

	grant := GrantKey{
		SessionID: req.SessionID,
		Action:    access.Action,
		Dir:       s.grantDir(access),
		Scope:     s.grantScope(access),
	}

	if !forced {
		if s.sessionPrompt(req.SessionID) == PromptAllow {
			return Decision{Outcome: OutcomeAllow, Reason: "session runs without prompting"}
		}
		if _, ok := s.sessionPermissions.Get(grant); ok {
			s.notify(req.ToolCallID, true, false)
			return Decision{Outcome: OutcomeAllow, Reason: "granted earlier in this session"}
		}
		if verdict.Matched && verdict.Action == RuleAllow {
			return Decision{Outcome: OutcomeAllow, Reason: verdict.Reason}
		}
		if reason, ok := s.withinScope(access); ok {
			return Decision{Outcome: OutcomeAllow, Reason: reason}
		}
	}

	if decision, refused := s.promptRefusal(req.SessionID, forcedReason); refused {
		return decision
	}

	ticket := &Ticket{
		sessionID:  req.SessionID,
		toolCallID: req.ToolCallID,
		access:     access,
		grant:      grant,
		forced:     forced,
		reason:     forcedReason,
	}
	return s.prompt(ctx, ticket, req.Preview)
}

// promptRefusal settles a request that must never reach a prompt. Two
// separate things can put it there: the configuration refuses whatever
// a prompt would have asked, or nothing is attached to the session to
// answer one. The second is the reason this exists — prompt below
// blocks on a channel only a reply can close, so an unattended session
// that reached it would stall until its context died, which for a
// headless run means burning the whole timeout on a question no one
// was ever going to see.
func (s *permissionService) promptRefusal(sessionID, forcedReason string) (Decision, bool) {
	switch {
	case s.SessionUnattended(sessionID):
		reason := "nothing is attached to this session to approve it"
		if forcedReason != "" {
			reason = forcedReason + "; " + reason
		}
		return Decision{Outcome: OutcomePolicyDeny, Reason: reason}, true
	case s.sessionPrompt(sessionID) == PromptDeny || s.policy.Prompt() == PromptDeny:
		reason := forcedReason
		if reason == "" {
			reason = "permission prompts are disabled"
		}
		return Decision{Outcome: OutcomePolicyDeny, Reason: reason}, true
	default:
		return Decision{}, false
	}
}

func (s *permissionService) prompt(ctx context.Context, ticket *Ticket, preview Preview) Decision {
	// Announce the request before queueing behind other prompts, so a
	// waiting call shows as pending rather than silently stalling.
	s.notify(ticket.toolCallID, false, false)

	release, ok := s.acquireSession(ctx, ticket.sessionID)
	if !ok {
		return Decision{Outcome: OutcomeCancelled, Reason: "cancelled while waiting to prompt"}
	}
	defer release()

	// A grant may have arrived while this request waited its turn.
	if !ticket.forced {
		if _, ok := s.sessionPermissions.Get(ticket.grant); ok {
			s.notify(ticket.toolCallID, true, false)
			return Decision{Outcome: OutcomeAllow, Reason: "granted earlier in this session"}
		}
	}

	description := preview.Description
	if description == "" {
		description = describe(ticket.access)
	}
	request := PermissionRequest{
		ID:          uuid.New().String(),
		SessionID:   ticket.sessionID,
		ToolCallID:  ticket.toolCallID,
		ToolName:    ticket.access.Tool,
		Action:      ticket.access.Action.String(),
		Path:        ticket.access.Path,
		Description: description,
		Params:      preview.Params,
	}

	respCh := make(chan bool, 1)
	s.pendingRequests.Set(request.ID, respCh)
	defer s.pendingRequests.Del(request.ID)
	s.pendingGrants.Set(request.ID, *ticket)
	defer s.pendingGrants.Del(request.ID)

	s.Publish(pubsub.CreatedEvent, request)

	select {
	case <-ctx.Done():
		return Decision{Outcome: OutcomeCancelled, Reason: "cancelled while waiting for approval"}
	case granted := <-respCh:
		if granted {
			return Decision{Outcome: OutcomeAllow, Reason: "approved by the user"}
		}
		return Decision{Outcome: OutcomeUserDeny, Reason: "denied by the user"}
	}
}

// forcedPrompt reports a request that must reach the user whatever the
// stored grants say. Analysis that could not decompose a command is
// treated the same as a dangerous one: not knowing what a command
// touches is a reason to ask, never a reason to allow.
func (s *permissionService) forcedPrompt(access Access) (bool, string) {
	if access.Action != ActionExecute || access.Command == "" {
		return false, ""
	}
	scan := shellscan.Scan(access.Command, s.commandDir(access))
	if scan.Opaque {
		return true, "command could not be analysed: " + scan.Reason
	}
	for _, segment := range scan.Segments {
		if len(segment.Words) == 0 {
			continue
		}
		if segment.Dangerous {
			return true, "command runs " + segment.Words[0] + ", which always needs approval"
		}
		// A vehicle carries whatever it is handed, so approving it once
		// says nothing about what it will run next time.
		if segment.Vehicle {
			return true, "command runs " + segment.Words[0] +
				", which can carry any other command"
		}
	}
	return false, ""
}

// grantDir is the directory a grant is tied to. Only a command has
// one: the same words mean different things in different places, so
// `go test ./...` approved in the workspace must not also approve it
// against a directory the user never saw.
//
// Resolved, so that two spellings of one directory share the approval
// the user already gave rather than asking twice.
func (s *permissionService) grantDir(access Access) string {
	if access.Action != ActionExecute {
		return ""
	}
	return resolvePath(s.commandDir(access), "")
}

// grantScope narrows an access to what a session grant may cover, so
// that approving one thing does not approve its neighbours.
func (s *permissionService) grantScope(access Access) string {
	switch access.Action {
	case ActionExecute:
		if access.Command == "" {
			return access.Path
		}
		return s.commandGrantScope(access.Command)
	case ActionNetwork:
		host := urlHost(access.URL)
		if access.Path == "" {
			return host
		}
		// A download that lands on disk is approved for where it
		// lands, not just for the host. The directory rather than the
		// file keeps a batch of downloads to one prompt.
		return host + " -> " + filepath.Dir(access.Path)
	case ActionMCP:
		if access.MCPTool == "" {
			return access.Server
		}
		return access.Server + "/" + access.MCPTool
	default:
		return access.Path
	}
}

// commandGrantScope picks the command words a grant covers. A safe verb
// grants its own prefix, an ordinary command grants its verb plus
// flags, and anything dangerous or able to carry another command is
// pinned to the exact string it was approved for.
func (s *permissionService) commandGrantScope(command string) string {
	scan := shellscan.Scan(command, s.workingDir)
	if scan.Opaque || len(scan.Segments) != 1 {
		return command
	}
	segment := scan.Segments[0]
	words := segment.Words

	n := len(words)
	if !segment.Dangerous && !segment.Vehicle {
		if safe := shellscan.SafePrefix(words); safe > 0 {
			n = safe
		} else {
			n = min(2, len(words))
			for n < len(words) && strings.HasPrefix(words[n], "-") {
				n++
			}
		}
	}

	scope := strings.Join(words[:n], " ")
	// The stored key must parse back to the very words it was cut from.
	// Otherwise a quoted argument holding a space or a metacharacter
	// could mint a key that later matches a different command.
	check := shellscan.Scan(scope, s.workingDir)
	if check.Opaque || len(check.Segments) != 1 ||
		!slices.Equal(check.Segments[0].Words, words[:n]) {
		return command
	}
	return scope
}

// acquireSession takes the session's prompt slot, or reports false when
// the caller's context ends first. Waiting in a select rather than on a
// mutex is what lets a cancelled call give up its place in the queue.
func (s *permissionService) acquireSession(ctx context.Context, sessionID string) (func(), bool) {
	gate := s.sessionGates.GetOrSet(sessionID, func() chan struct{} {
		return make(chan struct{}, 1)
	})
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, true
	case <-ctx.Done():
		return nil, false
	}
}

func (s *permissionService) sessionPrompt(sessionID string) PromptPolicy {
	policy, ok := s.sessionPrompts.Get(sessionID)
	if !ok {
		return PromptAsk
	}
	return policy
}

func (s *permissionService) SetSessionPromptPolicy(sessionID string, policy PromptPolicy) {
	s.sessionPrompts.Set(sessionID, policy)
}

func (s *permissionService) SetSessionUnattended(sessionID string, unattended bool) {
	s.sessionUnattended.Set(sessionID, unattended)
}

func (s *permissionService) SessionUnattended(sessionID string) bool {
	unattended, _ := s.sessionUnattended.Get(sessionID)
	return unattended
}

func (s *permissionService) notify(toolCallID string, granted, denied bool) {
	s.notificationBroker.Publish(pubsub.CreatedEvent, PermissionNotification{
		ToolCallID: toolCallID,
		Granted:    granted,
		Denied:     denied,
	})
}

// resolve atomically removes the pending request entry for the given
// permission and, if it was still pending, publishes exactly one
// PermissionNotification and forwards the outcome to the waiter on
// respCh. It returns true if this call resolved the request, false if
// it had already been resolved (e.g., by another concurrent caller) or
// the request ID is unknown.
//
// If onResolve is non-nil it runs after the pending entry has been
// taken but before the notification is published or the waiter is
// unblocked. This lets GrantPersistent record the session permission
// only when it actually wins the race, so a losing GrantPersistent
// that lost to a Deny does not leak an auto-approve entry.
//
// All three public resolution methods (Grant, GrantPersistent, Deny)
// route through this helper so multi-subscriber UIs can race safely:
// the first caller wins, the rest become no-ops.
func (s *permissionService) resolve(permission PermissionRequest, granted, denied bool, onResolve func()) bool {
	respCh, ok := s.pendingRequests.Take(permission.ID)
	if !ok {
		return false
	}

	if onResolve != nil {
		onResolve()
	}

	s.notify(permission.ToolCallID, granted, denied)

	// respCh is buffered (cap 1) and only ever has at most one sender
	// per request because Take removes the entry under the map lock,
	// so this send never blocks.
	respCh <- granted
	return true
}

func (s *permissionService) GrantPersistent(permission PermissionRequest) bool {
	// Record the persistent grant only if this call wins the
	// pending-request race. Otherwise a losing GrantPersistent that
	// lost to a Deny would still leave an auto-approve entry behind,
	// silently flipping later denied calls to allowed.
	return s.resolve(permission, true, false, func() {
		ticket, ok := s.pendingGrants.Get(permission.ID)
		// A forced request is one the user must see every time, so it
		// never mints a grant that would skip the next prompt.
		if !ok || ticket.forced {
			return
		}
		s.sessionPermissions.Set(ticket.grant, true)
	})
}

func (s *permissionService) Grant(permission PermissionRequest) bool {
	return s.resolve(permission, true, false, nil)
}

func (s *permissionService) Deny(permission PermissionRequest) bool {
	return s.resolve(permission, false, true, nil)
}

func (s *permissionService) SubscribeNotifications(ctx context.Context) <-chan pubsub.Event[PermissionNotification] {
	return s.notificationBroker.Subscribe(ctx)
}

func (s *permissionService) SetSkipRequests(skip bool) {
	s.skip.Store(skip)
}

func (s *permissionService) SkipRequests() bool {
	return s.skip.Load()
}

// withinScope reports the accesses that need no approval because they
// stay inside the directory the user pointed Angela at. Looking around
// the workspace is the job; reaching outside it is the user's business,
// and so is every write, every network call and every MCP call.
//
// A deny rule still outranks this: the ladder checks rules first, so
// `deny read **/.env` keeps working on a file sitting in the workspace.
func (s *permissionService) withinScope(access Access) (string, bool) {
	switch access.Action {
	case ActionRead, ActionList:
		if access.Path == "" {
			return "reads state Angela already holds", true
		}
		resolved := resolvePath(access.Path, s.workingDir)
		for _, root := range s.readRoots {
			if withinResolvedDir(resolved, root) {
				return "reads inside " + root, true
			}
		}
		return "", false

	case ActionExecute:
		if access.Command == "" {
			return "", false
		}
		// A command is only safe when every link in it is read-only and
		// touches nothing outside the working directory — starting with
		// the directory it runs in. Operands alone do not settle that:
		// a command carrying none of them still reads whatever sits
		// around it, so `git status` pointed at another checkout
		// reports on a project the user never opened.
		cwd := s.commandDir(access)
		if !withinResolvedDir(resolvePath(cwd, s.workingDir), s.workingDir) {
			return "", false
		}
		inside := func(path string) bool {
			return withinResolvedDir(resolvePath(path, cwd), s.workingDir)
		}
		if shellscan.Scan(access.Command, cwd).Safe(inside) {
			return "read-only command inside the working directory", true
		}
		return "", false

	default:
		return "", false
	}
}

// commandDir is the directory a command runs in, which is the path the
// bash tool resolved rather than the service's own working directory.
func (s *permissionService) commandDir(access Access) string {
	if access.Path != "" {
		return access.Path
	}
	return s.workingDir
}

// PolicyDenial reports the deny rule refusing this access, without
// prompting or recording anything.
func (s *permissionService) PolicyDenial(access Access) (Decision, bool) {
	return denialOf(s.policy.Evaluate(access, s.workingDir))
}

// denialOf turns a verdict into a refusal, so the gate and the early
// check that runs before it cannot drift apart.
func denialOf(verdict Verdict) (Decision, bool) {
	if verdict.Matched && verdict.Action == RuleDeny {
		return Decision{Outcome: OutcomePolicyDeny, Reason: verdict.Reason}, true
	}
	return Decision{}, false
}

// describe renders the fallback prompt text for a tool that does not
// supply its own preview.
func describe(access Access) string {
	switch access.Action {
	case ActionExecute:
		if access.Command != "" {
			return "Execute command: " + access.Command
		}
		return "Run " + access.Tool
	case ActionNetwork:
		return "Reach " + access.URL
	case ActionMCP:
		if access.MCPTool != "" {
			return "Call " + access.Server + "/" + access.MCPTool
		}
		return "Use MCP server " + access.Server
	case ActionEdit:
		return "Write " + access.Path
	case ActionList:
		return "List " + access.Path
	case ActionMerge:
		return "Merge this branch into the conversation it forked from"
	default:
		if access.Path == "" {
			return "Run " + access.Tool
		}
		return "Read " + access.Path
	}
}

func urlHost(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return raw
	}
	return parsed.Hostname()
}
