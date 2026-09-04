package backend

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	"github.com/NaturalSelect/angela/internal/skills"
	"github.com/NaturalSelect/angela/internal/version"
	"github.com/stretchr/testify/require"
)

func TestBackendVersionInfo(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)

	info := b.VersionInfo()
	require.Equal(t, version.Version, info.Version)
	require.Equal(t, version.Commit, info.Commit)
	require.Equal(t, version.BuildID, info.BuildID)
	require.Equal(t, runtime.Version(), info.GoVersion)
	require.Equal(t, fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH), info.Platform)
}

func TestBackendConfigAccessor(t *testing.T) {
	t.Parallel()

	cfg := &config.ConfigStore{}
	b := New(context.Background(), cfg, nil)
	require.Same(t, cfg, b.Config())
}

func TestBackendShutdown(t *testing.T) {
	t.Parallel()

	b, shutdowns := newTestBackend(t)
	b.Shutdown()

	require.Equal(t, int32(1), shutdowns.Load())
	b.mu.Lock()
	require.True(t, b.closing)
	b.mu.Unlock()
}

// TestBackendShutdown_NilFn confirms Shutdown tolerates a backend built
// without a shutdown callback (e.g. an embedder that never wired one).
func TestBackendShutdown_NilFn(t *testing.T) {
	t.Parallel()

	b := &Backend{
		workspaces: csync.NewMap[string, *Workspace](),
		pathIndex:  make(map[string]string),
		ctx:        context.Background(),
	}
	require.NotPanics(t, b.Shutdown)
	b.mu.Lock()
	require.True(t, b.closing)
	b.mu.Unlock()
}

func TestBackendListWorkspaces_Empty(t *testing.T) {
	t.Parallel()
	b, _ := newTestBackend(t)

	list := b.ListWorkspaces()
	require.NotNil(t, list)
	require.Empty(t, list)
}

// TestBackendListWorkspaces_And_GetWorkspaceProto uses a real,
// app.App-backed workspace since workspaceToProto (shared by both
// methods) dereferences ws.Cfg, which the lightweight synthetic
// workspaces from insertTestWorkspace do not set.
func TestBackendListWorkspaces_And_GetWorkspaceProto(t *testing.T) {
	b, ws, _ := newPublishingWorkspace(t)

	list := b.ListWorkspaces()
	require.Len(t, list, 1)
	require.Equal(t, ws.ID, list[0].ID)
	require.Equal(t, ws.Path, list[0].Path)

	got, err := b.GetWorkspaceProto(ws.ID)
	require.NoError(t, err)
	require.Equal(t, ws.ID, got.ID)
	require.Equal(t, ws.Path, got.Path)

	_, err = b.GetWorkspaceProto("nope")
	require.ErrorIs(t, err, ErrWorkspaceNotFound)
}

// TestDurationFromEnv covers every branch of the fallback parser:
// unset, a valid override (including the meaningful zero value),
// and unparseable/negative input that must fall back to the default.
func TestDurationFromEnv(t *testing.T) {
	const key = "ANGELA_TEST_DURATION_FROM_ENV"

	tests := []struct {
		name  string
		setTo string
		unset bool
		def   time.Duration
		want  time.Duration
	}{
		{name: "unset falls back to default", unset: true, def: 5 * time.Second, want: 5 * time.Second},
		{name: "valid seconds overrides default", setTo: "30", def: 5 * time.Second, want: 30 * time.Second},
		{name: "zero is meaningful, not treated as unset", setTo: "0", def: 5 * time.Second, want: 0},
		{name: "non-numeric falls back to default", setTo: "not-a-number", def: 5 * time.Second, want: 5 * time.Second},
		{name: "negative falls back to default", setTo: "-1", def: 5 * time.Second, want: 5 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.unset {
				t.Setenv(key, tc.setTo)
			}
			require.Equal(t, tc.want, durationFromEnv(key, tc.def))
		})
	}
}

func TestSkillStatesToProto(t *testing.T) {
	t.Parallel()

	require.Nil(t, skillStatesToProto(nil))
	require.Nil(t, skillStatesToProto([]*skills.SkillState{}))

	boom := errors.New("boom")
	states := []*skills.SkillState{
		{Name: "ok-skill", Path: "/tmp/ok/SKILL.md", State: skills.StateNormal},
		{Name: "bad-skill", Path: "/tmp/bad/SKILL.md", State: skills.StateError, Err: boom},
	}
	out := skillStatesToProto(states)
	require.Len(t, out, 2)

	require.Equal(t, "ok-skill", out[0].Name)
	require.Equal(t, "/tmp/ok/SKILL.md", out[0].Path)
	require.Empty(t, out[0].Error)

	require.Equal(t, "bad-skill", out[1].Name)
	require.Equal(t, "boom", out[1].Error)
}
