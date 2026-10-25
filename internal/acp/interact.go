package acp

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/NaturalSelect/angela/internal/question"
)

const (
	permAllowOnce    = "allow_once"
	permAllowAlways  = "allow_always"
	permReject       = "reject"
	permRejectAlways = "reject_always"

	otherSuffix = "__other"
)

// diffPreviewer is implemented by the permission parameters of every tool
// that edits a file.
type diffPreviewer interface {
	DiffPreview() (filePath, oldContent, newContent string)
}

func (p *pump) onPermission(ctx context.Context, req permission.PermissionRequest) {
	s, ok := p.route(ctx, req.SessionID)
	if !ok {
		p.ws.PermissionDeny(req)
		return
	}

	toolCallID := req.ToolCallID
	if req.SessionID != s.id {
		toolCallID = childToolCallID(req.SessionID, req.ToolCallID)
	}
	if !s.announced(toolCallID) {
		p.reconcile(ctx, s, req.SessionID)
	}

	go func() {
		resp, err := p.client.RequestPermission(ctx, permissionRequestFor(s.id, toolCallID, req, p.cwd))
		switch {
		case err != nil || resp.Cancelled:
			p.ws.PermissionDeny(req)
		case resp.OptionID == permAllowOnce:
			p.ws.PermissionGrant(req)
		case resp.OptionID == permAllowAlways:
			p.ws.PermissionGrantPersistent(req)
		case resp.OptionID == permRejectAlways:
			p.ws.PermissionDenyPersistent(req)
		default:
			p.ws.PermissionDeny(req)
		}
	}()
}

// NOTE: A denial without a reason ends the turn, as it does in the
// terminal UI. Both always options last for this process and session tree
// only, because Angela stores no permanent rule from a prompt.
func permissionRequestFor(sessionID, toolCallID string, req permission.PermissionRequest, cwd string) protocol.PermissionRequest {
	call := protocol.ToolCallUpdate{ID: toolCallID}
	if preview, ok := req.Params.(diffPreviewer); ok {
		path, oldContent, newContent := preview.DiffPreview()
		if path != "" {
			diff := protocol.ToolDiff{Path: absPath(cwd, path), NewText: newContent}
			if oldContent != "" {
				diff.OldText = &oldContent
			}
			call.Content = []protocol.ToolCallContent{diff}
		}
	}
	return protocol.PermissionRequest{
		SessionID: sessionID,
		ToolCall:  call,
		Options: []protocol.PermissionOption{
			{ID: permAllowOnce, Name: "Allow", Kind: protocol.PermissionKindAllowOnce},
			{ID: permAllowAlways, Name: "Always allow (this session)", Kind: protocol.PermissionKindAllowAlways},
			{ID: permReject, Name: "Deny", Kind: protocol.PermissionKindRejectOnce},
			{ID: permRejectAlways, Name: "Always deny (this session)", Kind: protocol.PermissionKindRejectAlways},
		},
	}
}

func (p *pump) onQuestion(ctx context.Context, req question.Request) {
	s, ok := p.route(ctx, req.SessionID)
	if !ok || !p.elicitForm {
		p.ws.QuestionCancel()
		return
	}

	askCtx := p.questions.open(ctx, req.ID)
	go func() {
		resp, err := p.client.Elicit(askCtx, elicitationFor(s.id, req))
		if !p.questions.claim(req.ID) {
			return
		}
		if err == nil && resp.Action == protocol.ElicitActionAccept {
			p.ws.QuestionAnswer(answersFrom(req.Questions, resp.Values))
			return
		}
		p.ws.QuestionCancel()
	}()
}

// questionGuard tracks the one batch of questions that can be open. The
// question service lets a new batch replace the old one, and answers carry
// no batch ID, so an answer to a replaced batch must be discarded.
type questionGuard struct {
	mu      sync.Mutex
	current string
	cancel  context.CancelFunc
}

// open makes id the current batch and returns a context that ends when the
// batch is dismissed or replaced.
func (g *questionGuard) open(parent context.Context, id string) context.Context {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.release()
	ctx, cancel := context.WithCancel(parent)
	g.current, g.cancel = id, cancel
	return ctx
}

func (g *questionGuard) dismissed(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.current == id {
		g.release()
	}
}

// claim reports whether id is still the open batch, and closes it.
func (g *questionGuard) claim(id string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.current != id {
		return false
	}
	g.release()
	return true
}

func (g *questionGuard) release() {
	if g.cancel != nil {
		g.cancel()
	}
	g.current, g.cancel = "", nil
}

// NOTE: Choice questions get a free-text field beside them, which keeps
// the "or write your own" behavior of the terminal UI. Every field is
// optional; a skipped question is reported to the model as skipped.
func elicitationFor(sessionID string, req question.Request) protocol.ElicitationRequest {
	var parts []string
	for _, text := range []string{req.ConfirmTitle, req.ConfirmDescription} {
		if text != "" {
			parts = append(parts, text)
		}
	}
	message := strings.Join(parts, "\n\n")
	if message == "" {
		message = "Angela has a question"
	}

	var fields []protocol.FormField
	for _, q := range req.Questions {
		field := protocol.FormField{Name: q.ID, Title: q.Text, Description: q.Description}
		switch q.Type {
		case question.TypeYesNo:
			field.Kind = protocol.FieldKindBoolean
		case question.TypeSingleChoice:
			field.Kind = protocol.FieldKindSingleSelect
			field.Options = choiceOptions(q.Choices)
		case question.TypeMultiChoice:
			field.Kind = protocol.FieldKindMultiSelect
			field.Options = choiceOptions(q.Choices)
		default:
			field.Kind = protocol.FieldKindText
		}
		fields = append(fields, field)
		if q.Type == question.TypeSingleChoice || q.Type == question.TypeMultiChoice {
			fields = append(fields, protocol.FormField{
				Name:  q.ID + otherSuffix,
				Title: "Other (optional)",
				Kind:  protocol.FieldKindText,
			})
		}
	}
	return protocol.ElicitationRequest{
		SessionID:  sessionID,
		ToolCallID: req.ToolCallID,
		Message:    message,
		Fields:     fields,
	}
}

func choiceOptions(choices []question.Choice) []protocol.FormOption {
	options := make([]protocol.FormOption, 0, len(choices))
	for _, c := range choices {
		options = append(options, protocol.FormOption{Value: c.ID, Title: c.Label})
	}
	return options
}

// answersFrom returns one answer per question, in question order, because
// the question tool matches answers to questions by position.
func answersFrom(qs []question.Question, values map[string]any) []question.Answer {
	answers := make([]question.Answer, 0, len(qs))
	for _, q := range qs {
		answer := question.Answer{QuestionID: q.ID}
		value := values[q.ID]
		other, _ := values[q.ID+otherSuffix].(string)

		switch q.Type {
		case question.TypeYesNo:
			if yes, ok := value.(bool); ok {
				answer.Yes = &yes
			}
		case question.TypeSingleChoice:
			if id, ok := value.(string); ok && id != "" && other == "" {
				answer.SelectedIDs = []string{id}
			}
			answer.FillInText = other
		case question.TypeMultiChoice:
			answer.SelectedIDs = stringList(value)
			answer.FillInText = other
		default:
			answer.FillInText, _ = value.(string)
		}
		answers = append(answers, answer)
	}
	return answers
}

func stringList(v any) []string {
	items, ok := v.([]any)
	if !ok {
		if strs, ok := v.([]string); ok {
			return slices.Clone(strs)
		}
		return nil
	}
	var out []string
	for _, item := range items {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
