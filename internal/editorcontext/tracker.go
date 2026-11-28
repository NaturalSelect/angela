// Package editorcontext follows what a VS Code window shows: the active
// file, the selected lines and the files looked at most recently. It
// feeds that to the model and to the TUI header.
package editorcontext

import (
	"context"
	"log/slog"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/NaturalSelect/angela/internal/filepathext"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/vscodemcp"
)

const (
	// maxRecentFiles bounds Snapshot.RecentFiles, the active file
	// included.
	maxRecentFiles = 5
	// seedTimeout bounds the one-off selection query that fills the
	// snapshot before VS Code pushes anything.
	seedTimeout = 5 * time.Second
	// defaultRetryDelay is how long to wait before reconnecting after a
	// first failure. Every further failure doubles it up to
	// maxRetryDelay.
	defaultRetryDelay = 5 * time.Second
	maxRetryDelay     = time.Minute
)

// Selection is the text selected in the active file.
type Selection struct {
	// StartLine and EndLine are 1-based and inclusive.
	StartLine, EndLine int
	// Text is what VS Code reported, untruncated.
	Text string
}

// Snapshot is what the VS Code window showed at one moment. The zero
// value means no window is connected.
type Snapshot struct {
	Connected bool
	// ActiveFile is relative to the working directory when it sits
	// inside it, and absolute otherwise.
	ActiveFile string
	// Selection is nil while the editor holds a bare cursor.
	Selection *Selection
	// RecentFiles lists the files that were active, newest first, the
	// active one included. It survives a disconnect.
	RecentFiles []string
}

// Tracker keeps a Snapshot of the VS Code window up to date for as long
// as it runs, reconnecting when the window goes away.
type Tracker struct {
	getenv     func(string) string
	workingDir string
	broker     *pubsub.Broker[Snapshot]
	retryDelay time.Duration

	mu    sync.Mutex
	state Snapshot

	cancel context.CancelFunc
	done   chan struct{}
}

// New returns a Tracker for the VS Code window whose workspace contains
// workingDir. It does nothing until Start. A nil getenv means os.Getenv.
func New(getenv func(string) string, workingDir string) *Tracker {
	return &Tracker{
		getenv:     getenv,
		workingDir: workingDir,
		broker:     pubsub.NewBroker[Snapshot](),
		retryDelay: defaultRetryDelay,
	}
}

// Start begins following the window in the background. It does nothing
// outside VS Code's integrated terminal.
func (t *Tracker) Start(ctx context.Context) {
	if !vscodemcp.InIntegratedTerminal(t.getenv) {
		return
	}
	ctx, t.cancel = context.WithCancel(ctx)
	t.done = make(chan struct{})
	go t.run(ctx)
}

// Snapshot returns the current state.
func (t *Tracker) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	snap := t.state
	snap.RecentFiles = slices.Clone(snap.RecentFiles)
	return snap
}

// Subscribe returns the snapshots published whenever the state changes.
func (t *Tracker) Subscribe(ctx context.Context) <-chan pubsub.Event[Snapshot] {
	return t.broker.Subscribe(ctx)
}

// Close stops following the window and ends every subscription.
func (t *Tracker) Close() {
	if t.cancel != nil {
		t.cancel()
		<-t.done
	}
	t.broker.Shutdown()
}

func (t *Tracker) run(ctx context.Context) {
	defer close(t.done)

	delay := t.retryDelay
	for {
		if t.follow(ctx) {
			delay = t.retryDelay
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(2*delay, maxRetryDelay)
	}
}

// follow holds one session and applies what it pushes. It reports
// whether the connection was ever established.
func (t *Tracker) follow(ctx context.Context) bool {
	client, err := vscodemcp.Discover(t.getenv, t.workingDir)
	if err != nil {
		slog.Debug("No VS Code window to follow", "error", err)
		return false
	}
	session, err := client.Watch(ctx)
	if err != nil {
		slog.Debug("Failed to follow VS Code window", "error", err)
		return false
	}
	defer session.Close()

	slog.Debug("Following VS Code window")
	t.seed(ctx, session)
	t.update(func(s *Snapshot) { s.Connected = true })

	for {
		select {
		case <-ctx.Done():
			return true
		case ev, ok := <-session.Events():
			if !ok {
				slog.Debug("VS Code window went away")
				t.update(func(s *Snapshot) {
					s.Connected = false
					s.ActiveFile = ""
					s.Selection = nil
				})
				return true
			}
			if changed, ok := ev.(vscodemcp.SelectionChanged); ok {
				t.apply(changed.Selection)
			}
		}
	}
}

// seed fills in the editor that is active before any notification
// arrives, since VS Code only pushes when the selection moves.
func (t *Tracker) seed(ctx context.Context, session *vscodemcp.Session) {
	ctx, cancel := context.WithTimeout(ctx, seedTimeout)
	defer cancel()

	sel, err := session.Selection(ctx)
	if err != nil {
		slog.Debug("Failed to read the VS Code selection", "error", err)
		return
	}
	if sel != nil && sel.Current {
		t.apply(*sel)
	}
}

// apply records sel as the state of the active editor. Editors that do
// not show a file on disk, such as untitled documents or output panels,
// are ignored.
func (t *Tracker) apply(sel vscodemcp.Selection) {
	if !strings.HasPrefix(sel.FileURL, "file:") || sel.FilePath == "" {
		return
	}
	file := t.displayPath(sel.FilePath)
	selection := toSelection(sel)
	t.update(func(s *Snapshot) {
		s.ActiveFile = file
		s.Selection = selection
		s.RecentFiles = moveToFront(s.RecentFiles, file)
	})
}

// update applies mutate to the state and publishes the result when it
// changed. mutate must not modify slices the state already holds.
func (t *Tracker) update(mutate func(*Snapshot)) {
	t.mu.Lock()
	defer t.mu.Unlock()

	next := t.state
	mutate(&next)
	if reflect.DeepEqual(next, t.state) {
		return
	}
	t.state = next
	snap := next
	snap.RecentFiles = slices.Clone(snap.RecentFiles)
	t.broker.Publish(pubsub.UpdatedEvent, snap)
}

func (t *Tracker) displayPath(path string) string {
	if t.workingDir == "" || !filepathext.SmartIsAbs(path) {
		return path
	}
	rel, err := filepath.Rel(t.workingDir, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return rel
}

func toSelection(sel vscodemcp.Selection) *Selection {
	if sel.Range.IsEmpty || sel.Text == "" {
		return nil
	}
	start, end := sel.Range.Start, sel.Range.End
	lastLine := end.Line
	// NOTE: Selecting whole lines ends at column 0 of the line after
	// them, which holds none of the selected text.
	if end.Character == 0 && end.Line > start.Line {
		lastLine--
	}
	return &Selection{StartLine: start.Line + 1, EndLine: lastLine + 1, Text: sel.Text}
}

// moveToFront returns a new list with file first, followed by the
// entries of files other than file, up to maxRecentFiles.
func moveToFront(files []string, file string) []string {
	out := make([]string, 0, maxRecentFiles)
	out = append(out, file)
	for _, f := range files {
		if f != file && len(out) < maxRecentFiles {
			out = append(out, f)
		}
	}
	return out
}
