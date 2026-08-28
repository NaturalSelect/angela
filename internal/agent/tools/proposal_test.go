package tools

import (
	"context"
	"strings"
	"sync"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

func proposalCtx(t *testing.T, sessionID string) context.Context {
	t.Helper()
	return context.WithValue(t.Context(), SessionIDContextKey, sessionID)
}

func proposalCall(name, input string) fantasy.ToolCall {
	return fantasy.ToolCall{ID: "call-1", Name: name, Input: input}
}

// A proposal belongs to one branch. Two branches drafting at once must
// not see each other's text, and the parent conversation forks a fresh
// session per dispatch, so the store is keyed on it.
func TestProposalStoreIsolatesSessions(t *testing.T) {
	t.Parallel()

	store := NewProposalStore()
	store.Set("s1", "first")
	store.Set("s2", "second")

	got1, ok := store.Get("s1")
	require.True(t, ok)
	require.Equal(t, "first", got1)

	got2, ok := store.Get("s2")
	require.True(t, ok)
	require.Equal(t, "second", got2)

	store.Discard("s1")
	_, ok = store.Get("s1")
	require.False(t, ok)

	got2, ok = store.Get("s2")
	require.True(t, ok, "discarding one branch must not touch another")
	require.Equal(t, "second", got2)
}

// Discard runs from a deferred cleanup that fires whether or not the
// branch ever drafted anything.
func TestProposalStoreDiscardsUnknownSession(t *testing.T) {
	t.Parallel()

	require.NotPanics(t, func() { NewProposalStore().Discard("never-drafted") })
}

func TestProposalStoreConcurrentAccess(t *testing.T) {
	t.Parallel()

	store := NewProposalStore()
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			store.Set("s1", strings.Repeat("x", i))
		}()
		go func() {
			defer wg.Done()
			store.Get("s1")
		}()
	}
	wg.Wait()
}

func TestProposalWriteStoresTheDocument(t *testing.T) {
	t.Parallel()

	store := NewProposalStore()
	resp, err := NewProposalWriteTool(store).Run(
		proposalCtx(t, "s1"),
		proposalCall(ProposalWriteToolName, `{"content":"# Plan\n\nStep one."}`),
	)
	require.NoError(t, err)
	require.False(t, resp.IsError)

	doc, ok := store.Get("s1")
	require.True(t, ok)
	require.Equal(t, "# Plan\n\nStep one.", doc)
}

// The point of drafting in place is that revising costs a patch instead
// of the whole document. Echoing the text back in the tool result would
// spend on the return path exactly what the edit saved on the way in.
func TestProposalWritesDoNotEchoTheDocument(t *testing.T) {
	t.Parallel()

	store := NewProposalStore()
	body := "the quick brown fox jumps over the lazy dog"

	written, err := NewProposalWriteTool(store).Run(
		proposalCtx(t, "s1"),
		proposalCall(ProposalWriteToolName, `{"content":"`+body+`"}`),
	)
	require.NoError(t, err)
	require.NotContains(t, written.Content, body)

	edited, err := NewProposalEditTool(store).Run(
		proposalCtx(t, "s1"),
		proposalCall(ProposalEditToolName, `{"old_string":"quick","new_string":"slow"}`),
	)
	require.NoError(t, err)
	require.NotContains(t, edited.Content, "brown fox",
		"an edit must confirm itself without reciting the document")
}

func TestProposalWriteRejectsEmptyContent(t *testing.T) {
	t.Parallel()

	resp, err := NewProposalWriteTool(NewProposalStore()).Run(
		proposalCtx(t, "s1"),
		proposalCall(ProposalWriteToolName, `{"content":""}`),
	)
	require.NoError(t, err)
	require.True(t, resp.IsError)
}

func TestProposalEditReplacesInPlace(t *testing.T) {
	t.Parallel()

	store := NewProposalStore()
	store.Set("s1", "ship it on Tuesday")

	resp, err := NewProposalEditTool(store).Run(
		proposalCtx(t, "s1"),
		proposalCall(ProposalEditToolName, `{"old_string":"Tuesday","new_string":"Friday"}`),
	)
	require.NoError(t, err)
	require.False(t, resp.IsError)

	doc, _ := store.Get("s1")
	require.Equal(t, "ship it on Friday", doc)
}

// A refused edit must leave the document exactly as it was, or the model
// has to retype it to recover from a typo in old_string.
func TestProposalEditLeavesDocumentAloneOnFailure(t *testing.T) {
	t.Parallel()

	store := NewProposalStore()
	store.Set("s1", "ship it on Tuesday")

	resp, err := NewProposalEditTool(store).Run(
		proposalCtx(t, "s1"),
		proposalCall(ProposalEditToolName, `{"old_string":"Wednesday","new_string":"Friday"}`),
	)
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "old_string not found")

	doc, _ := store.Get("s1")
	require.Equal(t, "ship it on Tuesday", doc)
}

func TestProposalEditNeedsAUniqueMatch(t *testing.T) {
	t.Parallel()

	store := NewProposalStore()
	store.Set("s1", "step\nstep\n")

	resp, err := NewProposalEditTool(store).Run(
		proposalCtx(t, "s1"),
		proposalCall(ProposalEditToolName, `{"old_string":"step","new_string":"phase"}`),
	)
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, "appears multiple times")

	doc, _ := store.Get("s1")
	require.Equal(t, "step\nstep\n", doc)
}

func TestProposalEditReplaceAll(t *testing.T) {
	t.Parallel()

	store := NewProposalStore()
	store.Set("s1", "step\nstep\n")

	resp, err := NewProposalEditTool(store).Run(
		proposalCtx(t, "s1"),
		proposalCall(ProposalEditToolName, `{"old_string":"step","new_string":"phase","replace_all":true}`),
	)
	require.NoError(t, err)
	require.False(t, resp.IsError)

	doc, _ := store.Get("s1")
	require.Equal(t, "phase\nphase\n", doc)
}

func TestProposalEditBeforeAnyDraft(t *testing.T) {
	t.Parallel()

	resp, err := NewProposalEditTool(NewProposalStore()).Run(
		proposalCtx(t, "s1"),
		proposalCall(ProposalEditToolName, `{"old_string":"a","new_string":"b"}`),
	)
	require.NoError(t, err)
	require.True(t, resp.IsError)
	require.Contains(t, resp.Content, ProposalWriteToolName,
		"the error has to name the tool that gets the model unstuck")
}

func TestProposalReadReturnsTheWholeDocument(t *testing.T) {
	t.Parallel()

	store := NewProposalStore()
	store.Set("s1", "# Plan\n\nStep one.\nStep two.")

	resp, err := NewProposalReadTool(store).Run(
		proposalCtx(t, "s1"),
		proposalCall(ProposalReadToolName, `{}`),
	)
	require.NoError(t, err)
	require.False(t, resp.IsError)
	require.Equal(t, "# Plan\n\nStep one.\nStep two.", resp.Content)
}

// An empty read has to say so. Returning "" reads as a successful call
// with a blank document, which is a different thing from never having
// drafted one.
func TestProposalReadOnEmptyDocument(t *testing.T) {
	t.Parallel()

	resp, err := NewProposalReadTool(NewProposalStore()).Run(
		proposalCtx(t, "s1"),
		proposalCall(ProposalReadToolName, `{}`),
	)
	require.NoError(t, err)
	require.NotEmpty(t, resp.Content)
	require.Contains(t, resp.Content, ProposalWriteToolName)
}

func TestProposalToolsNeedASession(t *testing.T) {
	t.Parallel()

	store := NewProposalStore()
	for _, tc := range []struct {
		tool  fantasy.AgentTool
		name  string
		input string
	}{
		{NewProposalWriteTool(store), ProposalWriteToolName, `{"content":"x"}`},
		{NewProposalEditTool(store), ProposalEditToolName, `{"old_string":"a","new_string":"b"}`},
		{NewProposalReadTool(store), ProposalReadToolName, `{}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := tc.tool.Run(t.Context(), proposalCall(tc.name, tc.input))
			require.Error(t, err)
		})
	}
}
