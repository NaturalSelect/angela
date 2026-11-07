package vscodemcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"sync"
)

// eventBuffer is how many events a Session holds for a reader that has
// fallen behind before it starts dropping the oldest.
const eventBuffer = 16

// Event is something VS Code pushes to a watching Session. It is one of
// SelectionChanged, DiagnosticsChanged or FileReferenceAdded.
type Event interface {
	isEvent()
}

// SelectionChanged reports that the selection in an editor moved. VS
// Code sends it after the selection has settled for a moment, not on
// every keystroke.
type SelectionChanged struct {
	Selection
}

// DiagnosticsChanged reports the files whose problems changed. A file
// whose problems were all fixed is listed with none.
type DiagnosticsChanged struct {
	Files []FileDiagnostics
}

// FileReferenceAdded reports that the user asked VS Code to send a file
// or a selection to this session ("Add File Reference").
type FileReferenceAdded struct {
	FilePath string `json:"filePath"`
	FileURL  string `json:"fileUrl"`
	// Range and SelectedText are unset when the whole file is
	// referenced.
	Range        *Range `json:"selection"`
	SelectedText string `json:"selectedText"`
}

func (SelectionChanged) isEvent()   {}
func (DiagnosticsChanged) isEvent() {}
func (FileReferenceAdded) isEvent() {}

// decodeEvent turns one of VS Code's notifications into an Event. It
// reports false for methods it doesn't know and for malformed params,
// since a private channel's notifications can change without notice.
func decodeEvent(method string, params json.RawMessage) (Event, bool) {
	switch method {
	case "selection_changed":
		var ev SelectionChanged
		if json.Unmarshal(params, &ev) != nil {
			return nil, false
		}
		return ev, true
	case "diagnostics_changed":
		var payload struct {
			URIs []FileDiagnostics `json:"uris"`
		}
		if json.Unmarshal(params, &payload) != nil {
			return nil, false
		}
		return DiagnosticsChanged{Files: payload.URIs}, true
	case "add_file_reference":
		var ev FileReferenceAdded
		if json.Unmarshal(params, &ev) != nil {
			return nil, false
		}
		return ev, true
	}
	return nil, false
}

// Session is one long-lived connection to VS Code. Unlike the one-shot
// methods on Client it receives the notifications VS Code pushes and
// can name itself, at the cost of holding the connection open until
// Close.
type Session struct {
	conn   *conn
	events chan Event

	mu     sync.Mutex
	closed bool
}

// Watch opens a Session. It tells VS Code which process it belongs to,
// so that VS Code can tie it to the terminal Angela runs in.
func (c *Client) Watch(ctx context.Context) (*Session, error) {
	s := &Session{events: make(chan Event, eventBuffer)}

	conn, err := c.connect(ctx, connectOptions{
		headers: map[string]string{
			"X-Copilot-Pid":        strconv.Itoa(os.Getpid()),
			"X-Copilot-Parent-Pid": strconv.Itoa(os.Getppid()),
		},
		onNotification: s.handleNotification,
	})
	if err != nil {
		return nil, fmt.Errorf("connect to VS Code MCP endpoint: %w", err)
	}
	s.conn = conn

	go func() {
		_ = conn.Wait()
		s.shutdown()
	}()
	return s, nil
}

// Events returns the notifications VS Code pushes, oldest first. A
// reader that falls behind loses the oldest ones. The channel is closed
// once the session ends, whether through Close or because VS Code went
// away.
func (s *Session) Events() <-chan Event { return s.events }

// Close ends the session.
func (s *Session) Close() error {
	err := s.conn.Close()
	s.shutdown()
	return err
}

func (s *Session) handleNotification(method string, params json.RawMessage) {
	ev, ok := decodeEvent(method, params)
	if !ok {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	// The lock makes this the only sender, so making room once is
	// enough to guarantee the second try succeeds.
	for {
		select {
		case s.events <- ev:
			return
		default:
		}
		select {
		case <-s.events:
		default:
		}
	}
}

func (s *Session) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.events)
	}
}
