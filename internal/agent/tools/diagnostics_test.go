package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/toolnames"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/stretchr/testify/require"
)

// newDiagLSPManager returns a *lsp.Manager with auto-start disabled so
// Start/notify calls in tests never shell out looking for a real LSP
// server (gopls, pyright, etc.) that may or may not be installed on the
// machine running the tests. No clients are ever registered, so every
// diagnostics.go function that ranges over manager.Clients() sees zero
// entries — a legitimate, common real-world state (no LSPs configured).
func newDiagLSPManager(t *testing.T) *lsp.Manager {
	t.Helper()
	autoLSP := false
	store := config.NewTestStore(&config.Config{
		Options: &config.Options{AutoLSP: &autoLSP},
	})
	return lsp.NewManager(store)
}

// runWithTimeout fails the test if fn does not return within d, guarding
// against a hang instead of waiting for the full test-binary timeout.
func runWithTimeout(t *testing.T, d time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatal("function did not return in time")
	}
}

func TestFormatDiagnostic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		diagnostic protocol.Diagnostic
		source     string
		want       string
	}{
		{
			name: "error severity",
			path: "main.go",
			diagnostic: protocol.Diagnostic{
				Range:    protocol.Range{Start: protocol.Position{Line: 4, Character: 2}},
				Message:  "undefined variable",
				Severity: protocol.SeverityError,
			},
			source: "gopls",
			want:   "Error: main.go:5:3 [gopls] undefined variable",
		},
		{
			name: "warning severity with diagnostic source appended",
			path: "main.go",
			diagnostic: protocol.Diagnostic{
				Range:    protocol.Range{Start: protocol.Position{Line: 0, Character: 0}},
				Message:  "unused import",
				Severity: protocol.SeverityWarning,
				Source:   "golint",
			},
			source: "gopls",
			want:   "Warn: main.go:1:1 [gopls golint] unused import",
		},
		{
			name: "hint severity",
			path: "a.go",
			diagnostic: protocol.Diagnostic{
				Range:    protocol.Range{Start: protocol.Position{Line: 9, Character: 9}},
				Message:  "consider simplifying",
				Severity: protocol.SeverityHint,
			},
			source: "gopls",
			want:   "Hint: a.go:10:10 [gopls] consider simplifying",
		},
		{
			name: "information severity falls back to Info",
			path: "a.go",
			diagnostic: protocol.Diagnostic{
				Range:    protocol.Range{Start: protocol.Position{Line: 0, Character: 0}},
				Message:  "note",
				Severity: protocol.SeverityInformation,
			},
			source: "gopls",
			want:   "Info: a.go:1:1 [gopls] note",
		},
		{
			name: "zero-value severity falls back to Info",
			path: "a.go",
			diagnostic: protocol.Diagnostic{
				Range:   protocol.Range{Start: protocol.Position{Line: 0, Character: 0}},
				Message: "note",
			},
			source: "gopls",
			want:   "Info: a.go:1:1 [gopls] note",
		},
		{
			name: "code is rendered in brackets",
			path: "a.go",
			diagnostic: protocol.Diagnostic{
				Range:    protocol.Range{Start: protocol.Position{Line: 0, Character: 0}},
				Message:  "bad thing",
				Severity: protocol.SeverityError,
				Code:     "undeclared-name",
			},
			source: "gopls",
			want:   "Error: a.go:1:1 [gopls][undeclared-name] bad thing",
		},
		{
			name: "numeric code is rendered in brackets",
			path: "a.go",
			diagnostic: protocol.Diagnostic{
				Range:    protocol.Range{Start: protocol.Position{Line: 0, Character: 0}},
				Message:  "bad thing",
				Severity: protocol.SeverityError,
				Code:     float64(42),
			},
			source: "gopls",
			want:   "Error: a.go:1:1 [gopls][42] bad thing",
		},
		{
			name: "unnecessary tag",
			path: "a.go",
			diagnostic: protocol.Diagnostic{
				Range:    protocol.Range{Start: protocol.Position{Line: 0, Character: 0}},
				Message:  "dead code",
				Severity: protocol.SeverityWarning,
				Tags:     []protocol.DiagnosticTag{protocol.Unnecessary},
			},
			source: "gopls",
			want:   "Warn: a.go:1:1 [gopls] (unnecessary) dead code",
		},
		{
			name: "deprecated tag",
			path: "a.go",
			diagnostic: protocol.Diagnostic{
				Range:    protocol.Range{Start: protocol.Position{Line: 0, Character: 0}},
				Message:  "old API",
				Severity: protocol.SeverityWarning,
				Tags:     []protocol.DiagnosticTag{protocol.Deprecated},
			},
			source: "gopls",
			want:   "Warn: a.go:1:1 [gopls] (deprecated) old API",
		},
		{
			name: "both tags combine in declared order",
			path: "a.go",
			diagnostic: protocol.Diagnostic{
				Range:    protocol.Range{Start: protocol.Position{Line: 0, Character: 0}},
				Message:  "old dead code",
				Severity: protocol.SeverityWarning,
				Tags:     []protocol.DiagnosticTag{protocol.Unnecessary, protocol.Deprecated},
			},
			source: "gopls",
			want:   "Warn: a.go:1:1 [gopls] (unnecessary, deprecated) old dead code",
		},
		{
			name: "unrecognized tag produces no tag suffix",
			path: "a.go",
			diagnostic: protocol.Diagnostic{
				Range:    protocol.Range{Start: protocol.Position{Line: 0, Character: 0}},
				Message:  "mystery",
				Severity: protocol.SeverityWarning,
				Tags:     []protocol.DiagnosticTag{99},
			},
			source: "gopls",
			want:   "Warn: a.go:1:1 [gopls] mystery",
		},
		{
			name: "code, source and tags all combine",
			path: "pkg/x.go",
			diagnostic: protocol.Diagnostic{
				Range:    protocol.Range{Start: protocol.Position{Line: 1, Character: 3}},
				Message:  "deprecated call",
				Severity: protocol.SeverityError,
				Source:   "staticcheck",
				Code:     "SA1019",
				Tags:     []protocol.DiagnosticTag{protocol.Deprecated},
			},
			source: "gopls",
			want:   "Error: pkg/x.go:2:4 [gopls staticcheck][SA1019] (deprecated) deprecated call",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := formatDiagnostic(tt.path, tt.diagnostic, tt.source)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestCountSeverity(t *testing.T) {
	t.Parallel()

	diags := []string{
		"Error: a.go:1:1 [gopls] bad",
		"Error: b.go:2:2 [gopls] worse",
		"Warn: c.go:3:3 [gopls] meh",
		"Hint: d.go:4:4 [gopls] fyi",
	}

	require.Equal(t, 2, countSeverity(diags, "Error"))
	require.Equal(t, 1, countSeverity(diags, "Warn"))
	require.Equal(t, 1, countSeverity(diags, "Hint"))
	require.Equal(t, 0, countSeverity(diags, "Info"))
	require.Equal(t, 0, countSeverity(nil, "Error"))
}

func TestSortDiagnostics(t *testing.T) {
	t.Parallel()

	in := []string{
		"Warn: z.go:1:1 [gopls] zzz",
		"Error: b.go:1:1 [gopls] bbb",
		"Warn: a.go:1:1 [gopls] aaa",
		"Error: a.go:1:1 [gopls] aaa",
	}

	got := sortDiagnostics(in)
	require.Equal(t, []string{
		"Error: a.go:1:1 [gopls] aaa",
		"Error: b.go:1:1 [gopls] bbb",
		"Warn: a.go:1:1 [gopls] aaa",
		"Warn: z.go:1:1 [gopls] zzz",
	}, got)
}

func TestWriteDiagnostics(t *testing.T) {
	t.Parallel()

	t.Run("empty input writes nothing", func(t *testing.T) {
		t.Parallel()
		var out strings.Builder
		writeDiagnostics(&out, "file_diagnostics", nil)
		require.Empty(t, out.String())
	})

	t.Run("small input has no truncation notice", func(t *testing.T) {
		t.Parallel()
		var out strings.Builder
		in := []string{"Error: a.go:1:1 [gopls] a", "Error: b.go:1:1 [gopls] b"}
		writeDiagnostics(&out, "file_diagnostics", in)
		got := out.String()
		require.Equal(t, "\n<file_diagnostics>\nError: a.go:1:1 [gopls] a\nError: b.go:1:1 [gopls] b\n</file_diagnostics>\n", got)
		require.NotContains(t, got, "more diagnostics")
	})

	t.Run("more than 10 entries truncates with a count", func(t *testing.T) {
		t.Parallel()
		var out strings.Builder
		in := make([]string, 12)
		for i := range in {
			in[i] = "Error: f.go:1:1 [gopls] item"
		}
		writeDiagnostics(&out, "project_diagnostics", in)
		got := out.String()
		require.Contains(t, got, "<project_diagnostics>")
		require.Contains(t, got, "</project_diagnostics>")
		require.Contains(t, got, "... and 2 more diagnostics")
		// Only the first 10 entries should be joined verbatim before the notice.
		require.Equal(t, 10, strings.Count(got, "Error: f.go:1:1 [gopls] item"))
	})
}

func TestGetDiagnosticsNilManager(t *testing.T) {
	t.Parallel()
	require.Empty(t, getDiagnostics("main.go", nil))
}

func TestGetDiagnosticsNoClients(t *testing.T) {
	t.Parallel()
	manager := newDiagLSPManager(t)

	require.Empty(t, getDiagnostics("", manager))
	require.Empty(t, getDiagnostics("/some/file.go", manager))
}

func TestNotifyLSPsNilManager(t *testing.T) {
	t.Parallel()
	runWithTimeout(t, 5*time.Second, func() {
		notifyLSPs(context.Background(), nil, "/some/file.go")
	})
}

func TestNotifyLSPsEmptyFilepathRefreshesWithNoClients(t *testing.T) {
	t.Parallel()
	manager := newDiagLSPManager(t)
	runWithTimeout(t, 5*time.Second, func() {
		notifyLSPs(context.Background(), manager, "")
	})
	require.Zero(t, manager.Clients().Len())
}

func TestNotifyLSPsWithFilepathAndNoClients(t *testing.T) {
	t.Parallel()
	manager := newDiagLSPManager(t)
	path := filepath.Join(t.TempDir(), "file.go")
	runWithTimeout(t, 5*time.Second, func() {
		notifyLSPs(context.Background(), manager, path)
	})
	require.Zero(t, manager.Clients().Len())
}

func TestOpenInLSPsGuards(t *testing.T) {
	t.Parallel()

	t.Run("empty filepath is a no-op", func(t *testing.T) {
		t.Parallel()
		manager := newDiagLSPManager(t)
		runWithTimeout(t, 5*time.Second, func() {
			openInLSPs(context.Background(), manager, "")
		})
	})

	t.Run("nil manager is a no-op", func(t *testing.T) {
		t.Parallel()
		runWithTimeout(t, 5*time.Second, func() {
			openInLSPs(context.Background(), nil, "/some/file.go")
		})
	})

	t.Run("valid manager with no clients completes", func(t *testing.T) {
		t.Parallel()
		manager := newDiagLSPManager(t)
		path := filepath.Join(t.TempDir(), "file.go")
		runWithTimeout(t, 5*time.Second, func() {
			openInLSPs(context.Background(), manager, path)
		})
	})
}

func TestWaitForLSPDiagnosticsGuards(t *testing.T) {
	t.Parallel()

	t.Run("empty filepath is a no-op", func(t *testing.T) {
		t.Parallel()
		manager := newDiagLSPManager(t)
		runWithTimeout(t, 5*time.Second, func() {
			waitForLSPDiagnostics(context.Background(), manager, "", time.Second)
		})
	})

	t.Run("nil manager is a no-op", func(t *testing.T) {
		t.Parallel()
		runWithTimeout(t, 5*time.Second, func() {
			waitForLSPDiagnostics(context.Background(), nil, "/some/file.go", time.Second)
		})
	})

	t.Run("non-positive timeout is a no-op", func(t *testing.T) {
		t.Parallel()
		manager := newDiagLSPManager(t)
		runWithTimeout(t, 5*time.Second, func() {
			waitForLSPDiagnostics(context.Background(), manager, "/some/file.go", 0)
			waitForLSPDiagnostics(context.Background(), manager, "/some/file.go", -time.Second)
		})
	})

	t.Run("valid manager with no clients completes without waiting the full timeout", func(t *testing.T) {
		t.Parallel()
		manager := newDiagLSPManager(t)
		path := filepath.Join(t.TempDir(), "file.go")
		runWithTimeout(t, 5*time.Second, func() {
			waitForLSPDiagnostics(context.Background(), manager, path, 10*time.Second)
		})
	})
}

func TestNewDiagnosticsToolEndToEnd(t *testing.T) {
	t.Parallel()

	manager := newDiagLSPManager(t)
	tool := NewDiagnosticsTool(manager)

	t.Run("project diagnostics with no file path", func(t *testing.T) {
		t.Parallel()
		input, err := json.Marshal(DiagnosticsParams{})
		require.NoError(t, err)

		var resp fantasy.ToolResponse
		runWithTimeout(t, 5*time.Second, func() {
			resp, err = tool.Run(context.Background(), fantasy.ToolCall{ID: "1", Name: toolnames.LSPDiagnostics, Input: string(input)})
		})
		require.NoError(t, err)
		require.False(t, resp.IsError)
		require.Empty(t, resp.Content)
	})

	t.Run("diagnostics scoped to a specific file", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "main.go")
		input, err := json.Marshal(DiagnosticsParams{FilePath: path})
		require.NoError(t, err)

		var resp fantasy.ToolResponse
		runWithTimeout(t, 5*time.Second, func() {
			resp, err = tool.Run(context.Background(), fantasy.ToolCall{ID: "2", Name: toolnames.LSPDiagnostics, Input: string(input)})
		})
		require.NoError(t, err)
		require.False(t, resp.IsError)
		require.Empty(t, resp.Content)
	})
}
