package agent

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/stretchr/testify/require"
)

func testSubSession(id, parent string, kind tools.SubSessionKind) tools.SubSession {
	return tools.SubSession{
		ID:        id,
		ParentID:  parent,
		Agent:     "task",
		Task:      "do " + id,
		Kind:      kind,
		StartedAt: time.Unix(1_700_000_000, 0),
	}
}

func addBackground(tree *subSessionTree, id, parent string) {
	tree.add(testSubSession(id, parent, tools.SubSessionKindBackground), func() {})
}

func requireBackgroundRunningUnder(t *testing.T, tree *subSessionTree, want map[string]int) {
	t.Helper()
	for sessionID, n := range want {
		require.Equal(t, n, tree.backgroundRunningUnder(sessionID), "running under %s", sessionID)
	}
}

func TestSubSessionTreeCountsBackgroundAgentsInEveryAncestor(t *testing.T) {
	t.Parallel()

	tree := newSubSessionTree()
	tree.add(testSubSession("mid", "root", tools.SubSessionKindForeground), nil)
	tree.add(testSubSession("leaf", "mid", tools.SubSessionKindForeground), nil)
	addBackground(tree, "a", "leaf")

	requireBackgroundRunningUnder(t, tree, map[string]int{"leaf": 1, "mid": 1, "root": 1, "elsewhere": 0})

	tree.end("a", tools.SubSessionCompleted)
	requireBackgroundRunningUnder(t, tree, map[string]int{"leaf": 0, "mid": 0, "root": 0})
}

func TestSubSessionTreeAddsUpAgentsAcrossABranchingTree(t *testing.T) {
	t.Parallel()

	tree := newSubSessionTree()
	tree.add(testSubSession("left", "root", tools.SubSessionKindForeground), nil)
	tree.add(testSubSession("right", "root", tools.SubSessionKindForeground), nil)
	addBackground(tree, "a", "left")
	addBackground(tree, "b", "left")
	addBackground(tree, "c", "right")
	addBackground(tree, "d", "root")

	requireBackgroundRunningUnder(t, tree, map[string]int{"left": 2, "right": 1, "root": 4})

	tree.end("a", tools.SubSessionFailed)
	tree.end("c", tools.SubSessionStopped)
	requireBackgroundRunningUnder(t, tree, map[string]int{"left": 1, "right": 0, "root": 2})
}

func TestSubSessionTreeCountsOnlyRunningBackgroundNodes(t *testing.T) {
	t.Parallel()

	tree := newSubSessionTree()
	tree.add(testSubSession("fg", "root", tools.SubSessionKindForeground), func() {})
	tree.add(testSubSession("br", "root", tools.SubSessionKindBranch), nil)
	addBackground(tree, "bg", "root")

	require.Equal(t, 1, tree.backgroundRunningUnder("root"))
}

func TestSubSessionTreeEndsARunningNodeOnlyOnce(t *testing.T) {
	t.Parallel()

	tree := newSubSessionTree()
	addBackground(tree, "a", "root")

	tree.end("a", tools.SubSessionCompleted)
	tree.end("a", tools.SubSessionFailed)
	tree.end("a", tools.SubSessionRunning)
	tree.end("missing", tools.SubSessionFailed)

	children := tree.childrenOf("root")
	require.Len(t, children, 1)
	require.Equal(t, tools.SubSessionCompleted, children[0].Status)
}

func TestSubSessionTreeStopsOnlyARunningNodeWithACancel(t *testing.T) {
	t.Parallel()

	tree := newSubSessionTree()
	running, branch, finished := &cancelSpy{}, &cancelSpy{}, &cancelSpy{}
	tree.add(testSubSession("running", "root", tools.SubSessionKindForeground), running.cancel)
	tree.add(testSubSession("branch", "root", tools.SubSessionKindBranch), nil)
	tree.add(testSubSession("finished", "root", tools.SubSessionKindForeground), finished.cancel)
	tree.end("finished", tools.SubSessionCompleted)

	require.True(t, tree.stop("running"))
	require.True(t, running.called)
	require.False(t, tree.stop("branch"))
	require.False(t, tree.stop("finished"))
	require.False(t, finished.called)
	require.False(t, tree.stop("missing"))
	require.False(t, branch.called)

	require.Equal(t, tools.SubSessionRunning, tree.childrenOf("root")[0].Status, "stop leaves the status to the run's own end")
}

func TestSubSessionTreeSnapshotsAreNeverChangedAfterwards(t *testing.T) {
	t.Parallel()

	tree := newSubSessionTree()
	addBackground(tree, "a", "root")
	addBackground(tree, "b", "root")

	snapshot := tree.childrenOf("root")
	tree.end("a", tools.SubSessionCompleted)
	addBackground(tree, "c", "root")
	tree.sessionDeleted("b")

	require.Len(t, snapshot, 2)
	require.Equal(t, tools.SubSessionRunning, snapshot[0].Status)
	require.Equal(t, "b", snapshot[1].ID)
}

func TestSubSessionTreeListsChildrenInDispatchOrder(t *testing.T) {
	t.Parallel()

	tree := newSubSessionTree()
	for _, id := range []string{"c", "a", "b"} {
		addBackground(tree, id, "root")
	}
	addBackground(tree, "nested", "a")

	children := tree.childrenOf("root")
	require.Len(t, children, 3)
	require.Equal(t, []string{"c", "a", "b"}, []string{children[0].ID, children[1].ID, children[2].ID})
	require.Empty(t, tree.childrenOf("b"))
}

func TestSubSessionTreeDeletingASessionDropsItsSubtree(t *testing.T) {
	t.Parallel()

	tree := newSubSessionTree()
	tree.add(testSubSession("mid", "root", tools.SubSessionKindForeground), nil)
	addBackground(tree, "a", "mid")
	addBackground(tree, "b", "other")

	tree.sessionDeleted("mid")

	requireBackgroundRunningUnder(t, tree, map[string]int{"mid": 0, "root": 0, "other": 1})
	require.Empty(t, tree.childrenOf("root"))
	require.Empty(t, tree.childrenOf("mid"))

	tree.end("a", tools.SubSessionStopped)
	require.False(t, tree.stop("a"))
	require.Empty(t, tree.childrenOf("mid"), "ending a dropped node does not bring it back")
}

func TestSubSessionTreeStaysConsistentWhenAnAncestorIsDeleted(t *testing.T) {
	t.Parallel()

	tree := newSubSessionTree()
	tree.add(testSubSession("mid", "root", tools.SubSessionKindForeground), nil)
	addBackground(tree, "a", "mid")

	tree.sessionDeleted("root")
	tree.end("a", tools.SubSessionCompleted)

	requireBackgroundRunningUnder(t, tree, map[string]int{"root": 0, "mid": 0})
}

func TestSubSessionTreeNilReceiverIsHarmless(t *testing.T) {
	t.Parallel()

	var tree *subSessionTree
	tree.add(testSubSession("a", "root", tools.SubSessionKindBackground), func() {})
	tree.end("a", tools.SubSessionCompleted)
	tree.sessionDeleted("a")
	require.False(t, tree.stop("a"))
	require.Empty(t, tree.childrenOf("root"))
	require.Zero(t, tree.backgroundRunningUnder("root"))
	require.NoError(t, tree.hydrate("root", func() ([]tools.SubSession, error) {
		return []tools.SubSession{testSubSession("x", "root", "")}, nil
	}))
}

func TestSubSessionTreeHydrateAddsEndedChildrenOnce(t *testing.T) {
	t.Parallel()

	tree := newSubSessionTree()
	earlier := testSubSession("old-1", "root", "")
	later := testSubSession("old-2", "root", "")
	later.StartedAt = earlier.StartedAt.Add(time.Minute)

	var loads int
	load := func() ([]tools.SubSession, error) {
		loads++
		return []tools.SubSession{later, earlier}, nil
	}
	require.NoError(t, tree.hydrate("root", load))
	require.NoError(t, tree.hydrate("root", load))
	require.Equal(t, 1, loads, "a loaded parent is not read again")

	children := tree.childrenOf("root")
	require.Len(t, children, 2)
	require.Equal(t, "old-1", children[0].ID, "hydrated children come back in start order")
	for _, c := range children {
		require.Equal(t, tools.SubSessionEnded, c.Status)
		require.Equal(t, tools.SubSessionKindUnknown, c.Kind)
		require.False(t, tree.stop(c.ID), "an ended child has nothing to cancel")
	}
	require.Zero(t, tree.backgroundRunningUnder("root"))
}

func TestSubSessionTreeHydrateKeepsNodesDispatchedByThisProcess(t *testing.T) {
	t.Parallel()

	tree := newSubSessionTree()
	addBackground(tree, "live", "root")

	require.NoError(t, tree.hydrate("root", func() ([]tools.SubSession, error) {
		return []tools.SubSession{
			testSubSession("live", "root", ""),
			testSubSession("old", "root", ""),
		}, nil
	}))

	children := tree.childrenOf("root")
	require.Len(t, children, 2)
	byID := map[string]tools.SubSession{}
	for _, c := range children {
		byID[c.ID] = c
	}
	require.Equal(t, tools.SubSessionRunning, byID["live"].Status)
	require.Equal(t, tools.SubSessionKindBackground, byID["live"].Kind)
	require.Equal(t, tools.SubSessionEnded, byID["old"].Status)
	require.Equal(t, 1, tree.backgroundRunningUnder("root"))
}

func TestSubSessionTreeHydrateKeepsADispatchThatLandsWhileLoading(t *testing.T) {
	t.Parallel()

	tree := newSubSessionTree()
	require.NoError(t, tree.hydrate("root", func() ([]tools.SubSession, error) {
		addBackground(tree, "racing", "root")
		return []tools.SubSession{testSubSession("racing", "root", ""), testSubSession("old", "root", "")}, nil
	}))

	byID := map[string]tools.SubSession{}
	for _, c := range tree.childrenOf("root") {
		byID[c.ID] = c
	}
	require.Len(t, byID, 2)
	require.Equal(t, tools.SubSessionRunning, byID["racing"].Status)
	require.Equal(t, tools.SubSessionEnded, byID["old"].Status)
}

func TestSubSessionTreeHydrateKeepsADispatchThatCreatesTheListConcurrently(t *testing.T) {
	t.Parallel()

	for i := range 20_000 {
		tree := newSubSessionTree()
		parent := fmt.Sprintf("root%d", i)
		start := make(chan struct{})

		var wg sync.WaitGroup
		wg.Go(func() {
			<-start
			addBackground(tree, "running", parent)
		})
		wg.Go(func() {
			<-start
			_ = tree.hydrate(parent, func() ([]tools.SubSession, error) { return nil, nil })
		})
		close(start)
		wg.Wait()

		require.Equal(t, 1, tree.backgroundRunningUnder(parent), "round %d", i)
		require.Len(t, tree.childrenOf(parent), 1, "round %d", i)
	}
}

func TestSubSessionTreeHydrateRetriesAfterALoadError(t *testing.T) {
	t.Parallel()

	tree := newSubSessionTree()
	require.Error(t, tree.hydrate("root", func() ([]tools.SubSession, error) {
		return nil, errors.New("database is locked")
	}))
	require.Empty(t, tree.childrenOf("root"))

	require.NoError(t, tree.hydrate("root", func() ([]tools.SubSession, error) {
		return []tools.SubSession{testSubSession("old", "root", "")}, nil
	}))
	require.Len(t, tree.childrenOf("root"), 1)
}

func TestSubSessionTreeHydrateIgnoresAParentDeletedWhileLoading(t *testing.T) {
	t.Parallel()

	tree := newSubSessionTree()
	require.NoError(t, tree.hydrate("root", func() ([]tools.SubSession, error) {
		tree.sessionDeleted("root")
		return []tools.SubSession{testSubSession("old", "root", "")}, nil
	}))

	require.Empty(t, tree.childrenOf("root"))
}

func TestSubSessionTreeIsExactUnderConcurrentDispatchAndEndings(t *testing.T) {
	t.Parallel()

	const parents = 16
	tree := newSubSessionTree()
	for p := range parents {
		tree.add(testSubSession(fmt.Sprintf("p%d", p), "root", tools.SubSessionKindForeground), nil)
	}

	var wg sync.WaitGroup
	for p := range parents {
		wg.Go(func() {
			parent := fmt.Sprintf("p%d", p)
			for i := range 50 {
				id := fmt.Sprintf("%s-%d", parent, i)
				addBackground(tree, id, parent)
				_ = tree.backgroundRunningUnder("root")
				_ = tree.childrenOf(parent)
				tree.end(id, tools.SubSessionCompleted)
			}
			addBackground(tree, parent+"-last", parent)
		})
	}
	wg.Wait()

	require.Equal(t, parents, tree.backgroundRunningUnder("root"), "only the last agent of each parent is left")
	for p := range parents {
		require.Equal(t, 1, tree.backgroundRunningUnder(fmt.Sprintf("p%d", p)))
	}
}

func newSubSessionsTestCoordinator(t *testing.T) *coordinator {
	t.Helper()
	env := testEnv(t)
	return newTestCoordinator(t, env, "test-provider", config.ProviderConfig{ID: "test-provider"})
}

func TestCoordinatorSubSessionsReportsChildrenOfAnEarlierProcessAsEnded(t *testing.T) {
	t.Parallel()

	coord := newSubSessionsTestCoordinator(t)
	parent, err := coord.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	oldID := coord.sessions.CreateAgentToolSessionID("msg-old", "call-old")
	_, err = coord.sessions.CreateTaskSession(t.Context(), oldID, parent.ID, "Survey the auth layer")
	require.NoError(t, err)
	untitledID := coord.sessions.CreateAgentToolSessionID("msg-untitled", "call-untitled")
	_, err = coord.sessions.CreateTaskSession(t.Context(), untitledID, parent.ID, defaultAgentSessionTitle)
	require.NoError(t, err)

	subs, err := coord.SubSessions(t.Context(), parent.ID)
	require.NoError(t, err)
	require.Len(t, subs, 2)
	require.Equal(t, oldID, subs[0].ID)
	require.Equal(t, parent.ID, subs[0].ParentID)
	require.Equal(t, "Survey the auth layer", subs[0].Task)
	require.Equal(t, tools.SubSessionEnded, subs[0].Status)
	require.Equal(t, tools.SubSessionKindUnknown, subs[0].Kind)
	require.Empty(t, subs[1].Task, "the placeholder title is not a task")
}

func TestCoordinatorSubSessionsKeepsTheLiveStatusOfAChildDispatchedHere(t *testing.T) {
	t.Parallel()

	coord := newSubSessionsTestCoordinator(t)
	parent, err := coord.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)
	liveID := coord.sessions.CreateAgentToolSessionID("msg-live", "call-live")
	_, err = coord.sessions.CreateTaskSession(t.Context(), liveID, parent.ID, "Live")
	require.NoError(t, err)
	oldID := coord.sessions.CreateAgentToolSessionID("msg-old", "call-old")
	_, err = coord.sessions.CreateTaskSession(t.Context(), oldID, parent.ID, "Old")
	require.NoError(t, err)

	live := testSubSession(liveID, parent.ID, tools.SubSessionKindBackground)
	coord.subSessions.add(live, func() {})

	subs, err := coord.SubSessions(t.Context(), parent.ID)
	require.NoError(t, err)
	byID := map[string]tools.SubSession{}
	for _, s := range subs {
		byID[s.ID] = s
	}
	require.Len(t, byID, 2)
	require.Equal(t, tools.SubSessionRunning, byID[liveID].Status)
	require.Equal(t, tools.SubSessionKindBackground, byID[liveID].Kind)
	require.Equal(t, tools.SubSessionEnded, byID[oldID].Status)
	require.Equal(t, 1, coord.BackgroundAgentsRunning(parent.ID))
}

func TestCoordinatorSubSessionsReadsTheDatabaseOncePerParent(t *testing.T) {
	t.Parallel()

	coord := newSubSessionsTestCoordinator(t)
	parent, err := coord.sessions.Create(t.Context(), "Parent")
	require.NoError(t, err)

	subs, err := coord.SubSessions(t.Context(), parent.ID)
	require.NoError(t, err)
	require.Empty(t, subs)

	lateID := coord.sessions.CreateAgentToolSessionID("msg-late", "call-late")
	_, err = coord.sessions.CreateTaskSession(t.Context(), lateID, parent.ID, "Created behind the tree's back")
	require.NoError(t, err)

	subs, err = coord.SubSessions(t.Context(), parent.ID)
	require.NoError(t, err)
	require.Empty(t, subs, "a loaded parent is not read again")
}

func TestCoordinatorSubSessionsCountsAnAgentLaunchedFromAnEarlierProcessSession(t *testing.T) {
	t.Parallel()

	coord := newSubSessionsTestCoordinator(t)
	root, err := coord.sessions.Create(t.Context(), "Root")
	require.NoError(t, err)
	childID := coord.sessions.CreateAgentToolSessionID("msg-child", "call-child")
	_, err = coord.sessions.CreateTaskSession(t.Context(), childID, root.ID, "Child")
	require.NoError(t, err)

	require.NoError(t, coord.adoptAncestors(t.Context(), childID))
	coord.subSessions.add(testSubSession("agent-session", childID, tools.SubSessionKindBackground), func() {})

	require.Equal(t, 1, coord.BackgroundAgentsRunning(childID))
	require.Equal(t, 1, coord.BackgroundAgentsRunning(root.ID), "the root sees what runs below its earlier-process child")

	require.Error(t, coord.adoptAncestors(t.Context(), "missing"))
}

func TestCoordinatorSubSessionsCountsAnAgentLaunchedBelowAChildTheUserListed(t *testing.T) {
	t.Parallel()

	coord := newSubSessionsTestCoordinator(t)
	root, err := coord.sessions.Create(t.Context(), "Root")
	require.NoError(t, err)
	midID := coord.sessions.CreateAgentToolSessionID("msg-mid", "call-mid")
	_, err = coord.sessions.CreateTaskSession(t.Context(), midID, root.ID, "Mid")
	require.NoError(t, err)
	leafID := coord.sessions.CreateAgentToolSessionID("msg-leaf", "call-leaf")
	_, err = coord.sessions.CreateTaskSession(t.Context(), leafID, midID, "Leaf")
	require.NoError(t, err)

	// Listing mid's children files the leaf, but nothing files mid itself.
	_, err = coord.SubSessions(t.Context(), midID)
	require.NoError(t, err)

	require.NoError(t, coord.adoptAncestors(t.Context(), leafID))
	coord.subSessions.add(testSubSession("agent-session", leafID, tools.SubSessionKindBackground), func() {})

	require.Equal(t, 1, coord.BackgroundAgentsRunning(midID))
	require.Equal(t, 1, coord.BackgroundAgentsRunning(root.ID), "a known session does not stop the walk to the root")
}
