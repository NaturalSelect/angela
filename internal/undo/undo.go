// Package undo reverts the file edits and conversation messages of a
// session's most recently completed turn: the user message(s) that
// started it, whatever the agent replied and did in response, and any
// subagent sessions it spawned along the way.
package undo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/NaturalSelect/angela/internal/filepathext"
	"github.com/NaturalSelect/angela/internal/filetracker"
	"github.com/NaturalSelect/angela/internal/fsext"
	"github.com/NaturalSelect/angela/internal/history"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/toolnames"
)

// ErrNothingToUndo reports that a session has no user message left to
// undo: either it is empty, or nothing in it is a user message.
var ErrNothingToUndo = errors.New("nothing to undo")

// ErrStale reports that the session changed between a Preview and the
// Undo that named its CutMessageID, so the turn Undo was asked to
// remove no longer matches what the session computes on its own.
// Undo recomputes the plan itself rather than trusting the caller's
// snapshot, so a stale request is refused instead of acting on
// outdated data.
var ErrStale = errors.New("session changed since the preview was taken")

// ErrSessionBusy reports that a session in scope for this turn — the
// one undo was asked about, or a subagent session its last turn
// spawned — still has agent activity this process is holding open: a
// turn in progress, or a branch whose parent tool call is suspended
// waiting on it. Undoing it would race that activity or delete a
// session out from under its suspended parent, so the whole plan is
// refused rather than computed against a turn that has not actually
// finished.
var ErrSessionBusy = errors.New("session has agent activity in progress")

// SkippedFile is a file undo left untouched, and why.
type SkippedFile struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Preview is what undoing a session's last turn would do, without
// doing it.
type Preview struct {
	// CutMessageID identifies the turn: it is the earliest message
	// Undo would remove, and must be passed back to Undo unchanged.
	CutMessageID string        `json:"cut_message_id"`
	PoppedText   string        `json:"popped_text"`
	MessageCount int           `json:"message_count"`
	Revert       []string      `json:"revert"`
	Delete       []string      `json:"delete"`
	Skipped      []SkippedFile `json:"skipped"`
}

// Result is what undoing a session's last turn actually did.
type Result struct {
	PoppedText   string        `json:"popped_text"`
	Reverted     []string      `json:"reverted"`
	Deleted      []string      `json:"deleted"`
	Skipped      []SkippedFile `json:"skipped"`
	MessageCount int           `json:"message_count"`
}

// Service rolls back the last turn of a session: the files its tools
// touched are restored to how they were before the turn (or deleted,
// if the turn created them), and the turn itself is removed from
// history so its user messages can be retyped and sent again.
//
// A turn is undone as a whole: partial undo of a single tool call
// within a turn is not supported. Only tracked file-editing tools
// (Edit, MultiEdit, Write, LSPRename, LSPReplaceSymbol) are reverted;
// other side effects, notably anything a Bash call did, are not.
type Service interface {
	// Preview reports what Undo would do without doing it.
	Preview(ctx context.Context, sessionID string) (Preview, error)

	// Undo reverts the turn identified by cutMessageID, as returned by
	// a prior Preview for the same session. It recomputes the plan
	// from scratch and returns ErrStale if the session no longer
	// agrees the turn starts there, rather than trusting the caller's
	// snapshot.
	Undo(ctx context.Context, sessionID, cutMessageID string) (Result, error)
}

// BusyChecker reports whether a session has agent activity in this
// process that undo must not disturb. agent.Coordinator satisfies it
// without this package importing agent.
type BusyChecker interface {
	IsSessionBusy(sessionID string) bool
	IsSessionBranch(sessionID string) bool
}

type service struct {
	messages    message.Service
	history     history.Service
	sessions    session.Service
	filetracker filetracker.Service
	busy        BusyChecker
	workingDir  string
}

// NewService builds the undo service. workingDir must match the value
// every tracked tool resolves relative file paths against, so a
// path recorded by a tool call and a path read back here name the
// same file.
func NewService(
	messages message.Service,
	history history.Service,
	sessions session.Service,
	filetracker filetracker.Service,
	busy BusyChecker,
	workingDir string,
) Service {
	return &service{
		messages:    messages,
		history:     history,
		sessions:    sessions,
		filetracker: filetracker,
		busy:        busy,
		workingDir:  workingDir,
	}
}

// checkBusy refuses to compute or apply a plan touching sessionID
// while this process still has agent activity open on it.
func (s *service) checkBusy(sessionID string) error {
	if s.busy.IsSessionBusy(sessionID) || s.busy.IsSessionBranch(sessionID) {
		return fmt.Errorf("%w: %s", ErrSessionBusy, sessionID)
	}
	return nil
}

func (s *service) Preview(ctx context.Context, sessionID string) (Preview, error) {
	p, resolved, err := s.resolve(ctx, sessionID)
	if err != nil {
		return Preview{}, err
	}

	preview := Preview{
		CutMessageID: p.cutMessageID,
		PoppedText:   p.poppedText,
		MessageCount: p.messageCount,
	}
	for _, rf := range resolved {
		switch {
		case rf.skip != "":
			preview.Skipped = append(preview.Skipped, SkippedFile{Path: rf.path, Reason: rf.skip})
		case rf.created:
			preview.Delete = append(preview.Delete, rf.path)
		default:
			preview.Revert = append(preview.Revert, rf.path)
		}
	}
	return preview, nil
}

func (s *service) Undo(ctx context.Context, sessionID, cutMessageID string) (Result, error) {
	p, resolved, err := s.resolve(ctx, sessionID)
	if err != nil {
		return Result{}, err
	}
	if p.cutMessageID != cutMessageID {
		return Result{}, ErrStale
	}

	result := Result{PoppedText: p.poppedText}

	// Files first, messages last: if a message delete were to fail
	// after files had already been reverted, the user still gets
	// their files back and can retry. The reverse order would risk
	// losing the conversation for nothing.
	for _, rf := range resolved {
		switch {
		case rf.skip != "":
			result.Skipped = append(result.Skipped, SkippedFile{Path: rf.path, Reason: rf.skip})
		case rf.created:
			if err := os.Remove(rf.path); err != nil && !os.IsNotExist(err) {
				result.Skipped = append(result.Skipped, SkippedFile{Path: rf.path, Reason: fmt.Sprintf("could not delete: %v", err)})
				continue
			}
			result.Deleted = append(result.Deleted, rf.path)
		default:
			if err := os.WriteFile(rf.path, []byte(rf.target), 0o644); err != nil {
				result.Skipped = append(result.Skipped, SkippedFile{Path: rf.path, Reason: fmt.Sprintf("could not write: %v", err)})
				continue
			}
			result.Reverted = append(result.Reverted, rf.path)
			// The file on disk is now exactly what the agent saw
			// before this turn, and that read is still further back
			// in history (undo never touches it), so recording it
			// again keeps the next edit from being refused as
			// "modified since read" against the turn we just removed.
			s.filetracker.RecordRead(ctx, sessionID, rf.path)
		}
	}

	if err := s.clearDanglingSummary(ctx, sessionID, p); err != nil {
		return Result{}, err
	}

	// Subagent sessions are not linked to their parent by a foreign
	// key, so nothing deletes them on its own; Undo must do it, or
	// they and their file history outlive the turn that created them.
	for _, childID := range p.childSessions {
		if err := s.sessions.Delete(ctx, childID); err != nil {
			slog.Error("Failed to delete subagent session during undo", "session_id", childID, "error", err)
		}
	}

	n, err := s.messages.DeleteFrom(ctx, sessionID, cutMessageID)
	if err != nil {
		return Result{}, err
	}
	result.MessageCount = n

	return result, nil
}

// clearDanglingSummary blanks the session's SummaryMessageID when it
// names a message this undo is about to delete, so a session is never
// left pointing at a summary that no longer exists. The context those
// messages held is not lost: undo never deletes the messages a
// summary was built from, only the turn after it.
func (s *service) clearDanglingSummary(ctx context.Context, sessionID string, p turnPlan) error {
	sess, err := s.sessions.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if sess.SummaryMessageID == "" || !p.deletedMessageIDs[sess.SummaryMessageID] {
		return nil
	}
	sess.SummaryMessageID = ""
	_, err = s.sessions.Save(ctx, sess)
	return err
}

// resolve computes the turn plan for sessionID and checks every file
// it touched against the current state of disk. Preview and Undo
// share it so the two can never disagree about what a turn would do.
func (s *service) resolve(ctx context.Context, sessionID string) (turnPlan, []resolvedFile, error) {
	if err := s.checkBusy(sessionID); err != nil {
		return turnPlan{}, nil, err
	}
	p, err := s.plan(ctx, sessionID)
	if err != nil {
		return turnPlan{}, nil, err
	}
	return p, s.resolveFiles(ctx, p.files), nil
}

// turnPlan is the fully resolved shape of a session's last turn: the
// message range it spans, the text to hand back to the editor, the
// subagent sessions it spawned, and what its tracked tool calls did
// to which files.
type turnPlan struct {
	cutMessageID      string
	poppedText        string
	messageCount      int
	deletedMessageIDs map[string]bool
	childSessions     []string
	files             map[string]fileAction
}

// plan locates sessionID's last turn and walks it for file actions.
// It returns ErrNothingToUndo if the session holds no user message.
func (s *service) plan(ctx context.Context, sessionID string) (turnPlan, error) {
	msgs, err := s.messages.List(ctx, sessionID)
	if err != nil {
		return turnPlan{}, err
	}

	cut := findCut(msgs)
	if cut < 0 {
		return turnPlan{}, ErrNothingToUndo
	}

	toDelete := msgs[cut:]
	deletedIDs := make(map[string]bool, len(toDelete))
	var poppedTexts []string
	for _, m := range toDelete {
		deletedIDs[m.ID] = true
		if m.Role == message.User {
			poppedTexts = append(poppedTexts, m.Content().Text)
		}
	}

	walk := &walkState{actions: make(map[string]fileAction)}
	if err := s.walkMessages(ctx, sessionID, toDelete, walk); err != nil {
		return turnPlan{}, err
	}

	return turnPlan{
		cutMessageID:      msgs[cut].ID,
		poppedText:        strings.Join(poppedTexts, "\n\n"),
		messageCount:      len(toDelete),
		deletedMessageIDs: deletedIDs,
		childSessions:     walk.childSessions,
		files:             walk.actions,
	}, nil
}

// findCut returns the index of the earliest message in the trailing
// run of user messages that drove the turn: any trailing
// assistant/tool messages are skipped first, since they are the
// turn's reply rather than its prompt, then consecutive user messages
// are collected back to the first non-user message or the top of the
// session. Returns -1 if msgs holds no user message at all.
func findCut(msgs []message.Message) int {
	i := len(msgs) - 1
	for i >= 0 && msgs[i].Role != message.User {
		i--
	}
	if i < 0 {
		return -1
	}
	for i > 0 && msgs[i-1].Role == message.User {
		i--
	}
	return i
}

// fileAction is what a walk decided happened to one file during the
// turn: the content to restore it to (or, if created, to delete it
// back to not existing), and which session's file history holds the
// content the walk most recently found for it.
type fileAction struct {
	target          string
	created         bool
	latestSessionID string
}

// walkState accumulates the result of walking a turn's messages,
// including every subagent session recursed into along the way.
type walkState struct {
	actions       map[string]fileAction
	childSessions []string
}

// walkMessages scans msgs newest first, recording what each tracked
// file-editing tool call did and recursing into any subagent session
// an Agent tool call spawned. Walking newest-to-oldest and always
// overwriting a path's target/created on every match, but only
// setting latestSessionID the first time the path is seen, means a
// path touched more than once resolves target/created to its oldest
// touch (the state to restore) while latestSessionID stays pinned to
// its newest touch (whose recorded history holds the drift-check
// baseline). Recursing into a subagent session at the point its Agent
// call appears keeps that ordering correct across session boundaries
// too: the whole subagent call completes atomically from its parent's
// point of view, so nothing else in the parent can be interleaved
// with it.
func (s *service) walkMessages(ctx context.Context, ownerSessionID string, msgs []message.Message, out *walkState) error {
	toolCalls := make(map[string]message.ToolCall)
	for _, msg := range msgs {
		for _, tc := range msg.ToolCalls() {
			toolCalls[tc.ID] = tc
		}
	}

	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]

		for _, tr := range msg.ToolResults() {
			for _, touch := range s.decodeToolResult(tr, toolCalls[tr.ToolCallID]) {
				a, seen := out.actions[touch.path]
				if !seen {
					a.latestSessionID = ownerSessionID
				}
				a.target = touch.target
				a.created = touch.created
				out.actions[touch.path] = a
			}
		}

		for _, tc := range msg.ToolCalls() {
			if tc.Name != toolnames.Agent {
				continue
			}
			childID := s.sessions.CreateAgentToolSessionID(msg.ID, tc.ID)
			child, err := s.sessions.Get(ctx, childID)
			if err != nil {
				// Never dispatched: refused, or the agent was
				// unavailable before a session could be created.
				continue
			}
			if err := s.checkBusy(child.ID); err != nil {
				return err
			}
			childMsgs, err := s.messages.List(ctx, child.ID)
			if err != nil {
				return fmt.Errorf("listing subagent session %s: %w", child.ID, err)
			}
			out.childSessions = append(out.childSessions, child.ID)
			if err := s.walkMessages(ctx, child.ID, childMsgs, out); err != nil {
				return err
			}
		}
	}
	return nil
}

// fileTouch is one tracked tool call's effect on one file.
type fileTouch struct {
	path    string
	target  string
	created bool
}

// decodeToolResult reports what a tool result did to which file(s),
// or nil if tr is not one of the tools undo can reason about, or its
// metadata failed to decode. Edit, MultiEdit and Write do not record
// their own path in the response metadata, so their path comes from
// re-decoding the matching call's input instead.
func (s *service) decodeToolResult(tr message.ToolResult, call message.ToolCall) []fileTouch {
	switch tr.Name {
	case toolnames.Edit:
		var meta tools.EditResponseMetadata
		if json.Unmarshal([]byte(tr.Metadata), &meta) != nil {
			return nil
		}
		if p := s.callFilePath(call); p != "" {
			return []fileTouch{{path: p, target: meta.OldContent, created: meta.Created}}
		}

	case toolnames.MultiEdit:
		var meta tools.MultiEditResponseMetadata
		if json.Unmarshal([]byte(tr.Metadata), &meta) != nil {
			return nil
		}
		if p := s.callFilePath(call); p != "" {
			return []fileTouch{{path: p, target: meta.OldContent, created: meta.Created}}
		}

	case toolnames.Write:
		var meta tools.WriteResponseMetadata
		if json.Unmarshal([]byte(tr.Metadata), &meta) != nil {
			return nil
		}
		if p := s.callFilePath(call); p != "" {
			return []fileTouch{{path: p, target: meta.OldContent, created: meta.Created}}
		}

	case toolnames.LSPReplaceSymbol:
		var meta tools.ReplaceSymbolResponseMetadata
		if json.Unmarshal([]byte(tr.Metadata), &meta) != nil || meta.FilePath == "" {
			return nil
		}
		// ReplaceSymbol cannot create a file: it operates on a symbol
		// an LSP already resolved, which requires the file to exist.
		return []fileTouch{{path: filepathext.SmartJoin(s.workingDir, meta.FilePath), target: meta.OldContent}}

	case toolnames.LSPRename:
		var meta tools.RenameResponseMetadata
		if json.Unmarshal([]byte(tr.Metadata), &meta) != nil {
			return nil
		}
		touches := make([]fileTouch, 0, len(meta.Files))
		for _, f := range meta.Files {
			if f.Path == "" {
				continue
			}
			// Rename only ever rewrites files a symbol search already
			// found, so it cannot create one either.
			touches = append(touches, fileTouch{path: filepathext.SmartJoin(s.workingDir, f.Path), target: f.OldContent})
		}
		return touches
	}
	return nil
}

// callFilePath decodes the file_path argument a model passed to an
// Edit, MultiEdit, or Write call, resolved against the working
// directory the same way the tool itself resolved it before touching
// disk or recording history, so the two name the same file.
func (s *service) callFilePath(call message.ToolCall) string {
	var params struct {
		FilePath string `json:"file_path"`
	}
	if call.ID == "" || json.Unmarshal([]byte(call.Input), &params) != nil || params.FilePath == "" {
		return ""
	}
	return filepathext.SmartJoin(s.workingDir, params.FilePath)
}

// resolvedFile is a file action checked against the current state of
// disk, ready to preview or apply.
type resolvedFile struct {
	path    string
	target  string
	created bool
	// skip explains why this file will not be touched. Empty means
	// undo can safely act on it.
	skip string
}

func (s *service) resolveFiles(ctx context.Context, files map[string]fileAction) []resolvedFile {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	slices.Sort(paths)

	resolved := make([]resolvedFile, 0, len(paths))
	for _, path := range paths {
		resolved = append(resolved, s.resolveFile(ctx, path, files[path]))
	}
	return resolved
}

// resolveFile checks one file's action against disk. A file undo
// last saw the agent leave in state X is safe to touch only if it is
// still in state X: anything else means something outside the
// session changed it since, and undoing would clobber that change.
func (s *service) resolveFile(ctx context.Context, path string, action fileAction) resolvedFile {
	rf := resolvedFile{path: path, target: action.target, created: action.created}

	expected, err := s.history.GetByPathAndSession(ctx, path, action.latestSessionID)
	if err != nil {
		rf.skip = "no recorded pre-turn content"
		return rf
	}
	expectedContent, _ := fsext.ToUnixLineEndings(expected.Content)

	disk, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			rf.skip = fmt.Sprintf("could not read file: %v", err)
			return rf
		}
		if !action.created {
			rf.skip = "deleted outside the session"
		}
		// A created file that is already gone needs nothing further:
		// disk already matches what undo would leave it as.
		return rf
	}

	diskContent, _ := fsext.ToUnixLineEndings(string(disk))
	if diskContent != expectedContent {
		rf.skip = "modified outside the session"
	}
	return rf
}
