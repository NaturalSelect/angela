package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/agent/tools"
	"github.com/stretchr/testify/require"
)

// TestRunKeepsWebFetchPagesAcrossTurns pins that a top-level turn leaves
// the session's saved web_fetch pages on disk. The model is handed their
// paths and may read them again on a later turn or after compaction, so
// ending a turn must not delete them.
func TestRunKeepsWebFetchPagesAcrossTurns(t *testing.T) {
	coord := newGateTestCoordinator(t, true)

	// The session has to exist: run returns before it reaches its
	// cleanup when the session cannot be resolved.
	sess, err := coord.sessions.Create(t.Context(), "web fetch scratch")
	require.NoError(t, err)

	scratchRoot := filepath.Join(coord.cfg.Config().Options.DataDirectory, "webfetch")
	scratch, err := tools.WebFetchScratchDir(scratchRoot, sess.ID)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(scratch, 0o700))
	page := filepath.Join(scratch, "page-1.md")
	require.NoError(t, os.WriteFile(page, []byte("saved"), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// The turn itself fails (the provider port is closed); only what it
	// leaves behind matters here.
	_, _ = coord.run(ctx, nil, sess.ID, "hello")

	require.FileExists(t, page)
}
