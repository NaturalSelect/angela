package acp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/message"
)

const (
	defaultCompletionGrace = 10 * time.Second
	maxAttachmentBytes     = 5 * 1024 * 1024
)

func (a *Agent) Prompt(ctx context.Context, req protocol.PromptRequest) (protocol.PromptResponse, error) {
	s, ok := a.reg.get(req.SessionID)
	if !ok {
		return protocol.PromptResponse{}, notFound("session %s not found", req.SessionID)
	}
	ws, _ := a.current()
	return runTurn(ctx, ws, a.client, s, req, a.completionGrace)
}

func (a *Agent) Cancel(_ context.Context, req protocol.CancelRequest) error {
	if s, ok := a.reg.get(req.SessionID); ok {
		ws, _ := a.current()
		cancelTurn(ws, s)
	}
	return nil
}

func cancelTurn(ws Workspace, s *liveSession) {
	s.signalCancelled()
	ws.AgentCancel(s.id)
}

// runTurn starts one run and waits for its RunComplete event.
//
// NOTE: ctx ends only when the connection closes. If the run's completion
// event never arrives, the turn still returns: shortly after the run
// reports back, or shortly after a cancel. A queued turn behind a busy
// session has no such signal of its own. It ends when the running turn
// finishes and hands off to it, when it is cancelled, or when the running
// turn reports a failure, which clears the queue.
//
// NOTE: A failed turn never hands off to its queue, and the prompts in it
// carry no completion hook, so each one would wait until the editor cancels
// it. Clearing reports every dropped prompt as cancelled, which ends those
// waits. It is done only for a run that reported its own failure. A run that
// failed before it became active may have prompts queued behind another run,
// and that run still hands off to them. A built-in command that fails, such
// as a compaction, is treated the same way once the session is idle again.
func runTurn(ctx context.Context, ws Workspace, c protocol.Client, s *liveSession, req protocol.PromptRequest, grace time.Duration) (protocol.PromptResponse, error) {
	// NOTE: The reservation comes first, so a cancel that lands while the
	// turn is still being set up, or while the run waits on MCP servers, is
	// recorded against it instead of being dropped.
	accept := ws.AgentBeginAccepted(ctx, s.id)
	cancelsBefore := s.cancelCount()

	active, _ := ws.AgentActive(ctx, s.id)
	text, attachments, err := promptInput(req.Prompt, active.CatwalkCfg.SupportsImages)
	if err != nil {
		accept.Close()
		return protocol.PromptResponse{}, err
	}
	exp, err := expandCommand(ctx, ws, s.id, text)
	if err != nil {
		accept.Close()
		return protocol.PromptResponse{}, err
	}
	if exp.act != nil {
		accept.Close()
		resp, err := runAction(ctx, c, s, exp, cancelsBefore)
		// NOTE: A failed action never hands off to its queue. If the
		// session is still busy the action did not own it, so the run that
		// does will hand off to those prompts and they must stay.
		if err != nil && !ws.AgentIsSessionBusy(s.id) {
			ws.AgentClearQueue(s.id)
		}
		return resp, err
	}
	text = exp.text
	attachments = append(attachments, exp.attachments...)

	runID := uuid.NewString()
	t := s.expect(runID)
	defer s.forget(runID)

	runCtx := agent.WithRunCompleteMarker(agent.WithRunID(ctx, runID))
	runDone := make(chan error, 1)
	go func() {
		defer accept.Close()
		runDone <- ws.AgentRunAccepted(runCtx, accept, s.id, text, attachments...)
	}()

	var (
		fallback  func() (protocol.PromptResponse, error)
		graceDone <-chan time.Time
		cancelled = t.cancelled
	)
	for {
		select {
		case end := <-t.done:
			resp, err := stopReasonOf(end)
			if err != nil {
				ws.AgentClearQueue(s.id)
			}
			return resp, err
		case err := <-runDone:
			runDone = nil
			if !agent.RunCompletePublished(runCtx) {
				if err != nil {
					return protocol.PromptResponse{}, runError(err)
				}
				continue
			}
			fallback = func() (protocol.PromptResponse, error) { return resultOf(err) }
			graceDone = time.After(grace)
		case <-cancelled:
			cancelled = nil
			fallback = func() (protocol.PromptResponse, error) {
				return protocol.PromptResponse{StopReason: protocol.StopReasonCancelled}, nil
			}
			if graceDone == nil {
				graceDone = time.After(grace)
			}
		case <-graceDone:
			slog.Warn("Run completion not received in time", "session_id", s.id, "run_id", runID)
			resp, err := fallback()
			if err != nil {
				ws.AgentClearQueue(s.id)
			}
			return resp, err
		case <-ctx.Done():
			return protocol.PromptResponse{}, ctx.Err()
		}
	}
}

// runAction carries out a built-in command that answers by itself, then
// shows its reply as the agent's message.
//
// NOTE: The action is registered as a turn so session/cancel reaches it. A
// cancel ends the action's context, which stops a compaction part-way. One
// that landed before the action registered is told apart by the cancel
// count, and the action is then not started at all.
func runAction(ctx context.Context, c protocol.Client, s *liveSession, exp expansion, cancelsBefore int) (protocol.PromptResponse, error) {
	runID := uuid.NewString()
	t := s.expect(runID)
	defer s.forget(runID)

	if s.cancelCount() > cancelsBefore {
		return protocol.PromptResponse{StopReason: protocol.StopReasonCancelled}, nil
	}

	actCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		select {
		case <-t.cancelled:
			stop()
		case <-actCtx.Done():
		}
	}()

	reply, err := exp.act(actCtx)
	select {
	case <-t.cancelled:
		return protocol.PromptResponse{StopReason: protocol.StopReasonCancelled}, nil
	default:
	}
	if ctx.Err() != nil {
		return protocol.PromptResponse{}, ctx.Err()
	}
	if err != nil {
		return protocol.PromptResponse{}, builtinError(exp.command, err)
	}
	if strings.TrimSpace(reply) != "" {
		note := protocol.SessionNotification{
			SessionID: s.id,
			Update:    protocol.AgentMessageChunk{Content: protocol.TextBlock{Text: reply}},
		}
		if err := c.SessionUpdate(ctx, note); err != nil {
			slog.Warn("Failed to send command reply", "session_id", s.id, "error", err)
		}
	}
	return protocol.PromptResponse{StopReason: protocol.StopReasonEndTurn}, nil
}

func runError(err error) error {
	return internalError("%v", err)
}

func resultOf(err error) (protocol.PromptResponse, error) {
	switch {
	case err == nil:
		return protocol.PromptResponse{StopReason: protocol.StopReasonEndTurn}, nil
	case errors.Is(err, context.Canceled):
		return protocol.PromptResponse{StopReason: protocol.StopReasonCancelled}, nil
	default:
		return protocol.PromptResponse{}, runError(err)
	}
}

// stopReasonOf prefers cancellation over an error: the protocol wants a
// cancelled turn to finish normally.
//
// NOTE: Angela has no turn limit, so max_turn_requests never occurs.
func stopReasonOf(end turnEnd) (protocol.PromptResponse, error) {
	switch {
	case end.rc.Cancelled:
		return protocol.PromptResponse{StopReason: protocol.StopReasonCancelled}, nil
	case end.rc.Error != "":
		return protocol.PromptResponse{}, internalError("%s", end.rc.Error)
	case end.rc.Truncated:
		return protocol.PromptResponse{StopReason: protocol.StopReasonMaxTokens}, nil
	case end.finish == message.FinishReasonContentFilter:
		return protocol.PromptResponse{StopReason: protocol.StopReasonRefusal}, nil
	default:
		return protocol.PromptResponse{StopReason: protocol.StopReasonEndTurn}, nil
	}
}

// promptInput turns the editor's content blocks into prompt text and
// attachments.
//
// NOTE: An image the model cannot accept is an error here. Angela would
// otherwise drop it silently and the user would think the model saw it.
func promptInput(blocks []protocol.ContentBlock, supportsImages bool) (string, []message.Attachment, error) {
	var (
		texts       []string
		links       []string
		attachments []message.Attachment
		images      int
	)
	for _, block := range blocks {
		switch b := block.(type) {
		case protocol.TextBlock:
			texts = append(texts, b.Text)
		case protocol.ImageBlock:
			if !supportsImages {
				return "", nil, invalidParams("the current model does not accept images")
			}
			if len(b.Data) > maxAttachmentBytes {
				return "", nil, invalidParams("image exceeds the %d MiB limit", maxAttachmentBytes>>20)
			}
			images++
			attachments = append(attachments, message.Attachment{
				FileName: fmt.Sprintf("image-%d.%s", images, imageExtension(b.MIMEType)),
				MimeType: b.MIMEType,
				Content:  b.Data,
			})
		case protocol.ResourceBlock:
			att, link, err := resourceInput(b, supportsImages)
			if err != nil {
				return "", nil, err
			}
			if link != "" {
				links = append(links, link)
			} else {
				attachments = append(attachments, att)
			}
		case protocol.ResourceLinkBlock:
			links = append(links, "@"+resourcePath(b.URI))
		}
	}

	prompt := strings.Join(append(texts, links...), "\n")
	if strings.TrimSpace(prompt) == "" && len(attachments) == 0 {
		return "", nil, invalidParams("the prompt is empty")
	}
	return prompt, attachments, nil
}

// resourceInput attaches an embedded resource when Angela can read its type
// inline, and otherwise leaves the model a reference to open itself.
func resourceInput(b protocol.ResourceBlock, supportsImages bool) (message.Attachment, string, error) {
	mime := cmp.Or(b.MIMEType, "text/plain")
	content := b.Blob
	if content == nil {
		content = []byte(b.Text)
	}

	isImage := strings.HasPrefix(mime, "image/")
	if !isImage && !strings.HasPrefix(mime, "text/") {
		return message.Attachment{}, "@" + resourcePath(b.URI), nil
	}
	if isImage && !supportsImages {
		return message.Attachment{}, "", invalidParams("the current model does not accept images")
	}
	if len(content) > maxAttachmentBytes {
		return message.Attachment{}, "", invalidParams("%s exceeds the %d MiB limit", b.URI, maxAttachmentBytes>>20)
	}

	path := resourcePath(b.URI)
	return message.Attachment{
		FilePath: path,
		FileName: baseName(path),
		MimeType: mime,
		Content:  content,
	}, "", nil
}

func resourcePath(uri string) string {
	return fileURIPath(uri, runtime.GOOS == "windows")
}

// fileURIPath turns a file:// URI into a path the tools can open.
//
// NOTE: Windows editors send file:///C:/proj/main.go, whose URL path is
// /C:/proj/main.go. Windows treats a leading slash as absolute, so left
// alone the path would point at the current drive's root instead of C:.
func fileURIPath(uri string, windows bool) string {
	if !strings.HasPrefix(uri, "file://") {
		return uri
	}
	u, err := url.Parse(uri)
	if err != nil || u.Path == "" {
		return uri
	}
	if !windows {
		return u.Path
	}

	path := u.Path
	if len(path) >= 3 && path[0] == '/' && path[2] == ':' && isASCIILetter(path[1]) {
		path = path[1:]
	} else if u.Host != "" && u.Host != "localhost" {
		path = "//" + u.Host + path
	}
	return strings.ReplaceAll(path, "/", `\`)
}

func isASCIILetter(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

func imageExtension(mime string) string {
	switch ext := strings.TrimPrefix(mime, "image/"); ext {
	case "jpeg":
		return "jpg"
	case "":
		return "img"
	default:
		return strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				return r
			}
			return -1
		}, ext)
	}
}
