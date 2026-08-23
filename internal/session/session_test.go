package session

import (
	"sync"
	"testing"

	"github.com/NaturalSelect/angela/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstimatedUsageStateSurvivesFetchModifySave(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "test")
	require.NoError(t, err)
	created.PromptTokens = 100
	created.CompletionTokens = 50
	created.EstimatedUsage = true

	saved, err := sessions.Save(t.Context(), created)
	require.NoError(t, err)
	require.True(t, saved.EstimatedUsage)

	fetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.True(t, fetched.EstimatedUsage)

	fetched.Todos = []Todo{{
		Content:    "Check estimate state",
		Status:     TodoStatusInProgress,
		ActiveForm: "Checking estimate state",
	}}

	updated, err := sessions.Save(t.Context(), fetched)
	require.NoError(t, err)
	require.True(t, updated.EstimatedUsage)

	refetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.True(t, refetched.EstimatedUsage)
}

func TestEstimatedUsageStateCanBeClearedByExplicitSave(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "test")
	require.NoError(t, err)
	created.PromptTokens = 100
	created.CompletionTokens = 50
	created.EstimatedUsage = true

	saved, err := sessions.Save(t.Context(), created)
	require.NoError(t, err)
	require.True(t, saved.EstimatedUsage)

	saved.EstimatedUsage = false
	updated, err := sessions.Save(t.Context(), saved)
	require.NoError(t, err)
	require.False(t, updated.EstimatedUsage)

	refetched, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.False(t, refetched.EstimatedUsage)
}

// AddCost is applied to ancestors while their own turn is writing title,
// tokens and todos. It must therefore write cost and nothing else — the
// Get-add-Save it replaced carried a whole stale row back to SQLite.
func TestAddCostWritesOnlyTheCost(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "original title")
	require.NoError(t, err)
	created.PromptTokens = 100
	created.CompletionTokens = 50
	created.Cost = 0.25
	created.Todos = []Todo{{
		Content:    "keep me",
		Status:     TodoStatusInProgress,
		ActiveForm: "keeping me",
	}}
	before, err := sessions.Save(t.Context(), created)
	require.NoError(t, err)

	require.NoError(t, sessions.AddCost(t.Context(), created.ID, 0.75))

	after, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)

	require.InDelta(t, 1.0, after.Cost, 1e-9)
	require.Equal(t, before.Title, after.Title)
	require.Equal(t, before.PromptTokens, after.PromptTokens)
	require.Equal(t, before.CompletionTokens, after.CompletionTokens)
	require.Equal(t, before.Todos, after.Todos)
	require.Equal(t, before.SummaryMessageID, after.SummaryMessageID)
}

// A cost roll-up onto a session that is already gone must say so rather
// than silently discarding the amount.
func TestAddCostOnAMissingSessionReportsIt(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	err = sessions.AddCost(t.Context(), "no-such-session", 0.5)
	require.ErrorIs(t, err, ErrSessionNotFound)
}

// Sibling sub-sessions reach a shared ancestor concurrently. Every
// increment has to survive; a read-add-write keeps only the last.
func TestConcurrentAddCostKeepsEveryIncrement(t *testing.T) {
	dataDir := t.TempDir()
	t.Cleanup(func() {
		require.NoError(t, db.Release(dataDir))
		db.ResetPool()
	})

	conn, err := db.Connect(t.Context(), dataDir)
	require.NoError(t, err)

	sessions := NewService(db.New(conn), conn)

	created, err := sessions.Create(t.Context(), "parent")
	require.NoError(t, err)

	const writers = 16
	var wg sync.WaitGroup
	for range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NoError(t, sessions.AddCost(t.Context(), created.ID, 0.01))
		}()
	}
	wg.Wait()

	after, err := sessions.Get(t.Context(), created.ID)
	require.NoError(t, err)
	require.InDelta(t, writers*0.01, after.Cost, 1e-9,
		"concurrent increments overwrote each other")
}
