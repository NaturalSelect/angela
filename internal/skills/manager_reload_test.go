package skills

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeTestSkill(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	body := "---\nname: " + name + "\ndescription: Skill " + name + " for reload tests.\n---\nDo a thing.\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, SkillFileName), []byte(body), 0o644))
}

func activeNames(mgr *Manager) []string {
	var names []string
	for _, s := range mgr.ActiveSkills() {
		names = append(names, s.Name)
	}
	return names
}

func TestManager_ReloadWithoutSource(t *testing.T) {
	t.Parallel()

	mgr := NewManager(nil, nil, nil)
	t.Cleanup(mgr.Shutdown)

	_, err := mgr.Reload()
	require.ErrorIs(t, err, ErrNoDiscoverySource)
}

func TestManager_ReloadPicksUpAddedAndRemovedSkills(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestSkill(t, root, "alpha")

	var mu sync.Mutex
	cfg := DiscoveryConfig{SkillsPaths: []string{root}, WorkingDir: "/work-1"}
	source := func() DiscoveryConfig {
		mu.Lock()
		defer mu.Unlock()
		return cfg
	}

	all, active, states := DiscoverFromConfig(source())
	mgr := NewManager(all, active, states, WithDiscoverySource(source))
	t.Cleanup(mgr.Shutdown)
	require.Contains(t, activeNames(mgr), "alpha")
	require.NotContains(t, activeNames(mgr), "beta")

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	events := mgr.SubscribeEvents(ctx)

	writeTestSkill(t, root, "beta")
	require.NoError(t, os.RemoveAll(filepath.Join(root, "alpha")))
	mu.Lock()
	cfg.WorkingDir = "/work-2"
	mu.Unlock()

	diff, err := mgr.Reload()
	require.NoError(t, err)
	require.Equal(t, []string{"beta"}, diff.Added)
	require.Equal(t, []string{"alpha"}, diff.Removed)
	require.Equal(t, diff.Before, diff.After)

	require.Contains(t, activeNames(mgr), "beta")
	require.NotContains(t, activeNames(mgr), "alpha")
	require.Equal(t, []string{root}, mgr.ResolvedPaths())
	require.Equal(t, "/work-2", mgr.WorkingDir())

	activeSnap, pathsSnap, wd := mgr.Snapshot()
	require.Equal(t, mgr.ActiveSkills(), activeSnap)
	require.Equal(t, []string{root}, pathsSnap)
	require.Equal(t, "/work-2", wd)

	select {
	case ev := <-events:
		require.NotEmpty(t, ev.Payload.States)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reload event")
	}
}

func TestManager_ReloadHonorsDisabledSkills(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestSkill(t, root, "alpha")

	var mu sync.Mutex
	cfg := DiscoveryConfig{SkillsPaths: []string{root}}
	source := func() DiscoveryConfig {
		mu.Lock()
		defer mu.Unlock()
		return cfg
	}
	mgr := NewManager(nil, nil, nil, WithDiscoverySource(source))
	t.Cleanup(mgr.Shutdown)

	_, err := mgr.Reload()
	require.NoError(t, err)
	require.Contains(t, activeNames(mgr), "alpha")

	mu.Lock()
	cfg.DisabledSkills = []string{"alpha"}
	mu.Unlock()

	diff, err := mgr.Reload()
	require.NoError(t, err)
	require.Equal(t, []string{"alpha"}, diff.Removed)
	require.NotContains(t, activeNames(mgr), "alpha")

	var found bool
	for _, s := range mgr.AllSkills() {
		found = found || s.Name == "alpha"
	}
	require.True(t, found, "disabled skills stay in the all list")
}

func TestManager_ConcurrentReloadAndReads(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeTestSkill(t, root, "alpha")
	source := func() DiscoveryConfig {
		return DiscoveryConfig{SkillsPaths: []string{root}, WorkingDir: "/work"}
	}
	mgr := NewManager(nil, nil, nil, WithDiscoverySource(source))
	t.Cleanup(mgr.Shutdown)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 5 {
				_, err := mgr.Reload()
				require.NoError(t, err)
			}
		})
		wg.Go(func() {
			for range 50 {
				_ = mgr.AllSkills()
				_ = mgr.ActiveSkills()
				_ = mgr.ResolvedPaths()
				_ = mgr.WorkingDir()
				_, _, _ = mgr.Snapshot()
				_ = mgr.States()
			}
		})
	}
	wg.Wait()
}

func TestManager_ReloadMirrorsGlobalStates(t *testing.T) {
	// Not parallel - touches package-level cache.
	prev := GetLatestStates()
	t.Cleanup(func() { SetLatestStates(prev) })
	SetLatestStates(nil)

	root := t.TempDir()
	writeTestSkill(t, root, "alpha")
	source := func() DiscoveryConfig {
		return DiscoveryConfig{SkillsPaths: []string{root}}
	}
	mgr := NewManager(nil, nil, nil, WithGlobalMirror(), WithDiscoverySource(source))
	t.Cleanup(mgr.Shutdown)

	_, err := mgr.Reload()
	require.NoError(t, err)

	var found bool
	for _, st := range GetLatestStates() {
		found = found || st.Name == "alpha"
	}
	require.True(t, found, "reload must refresh the package-level cache")
}

func TestDiffActive(t *testing.T) {
	t.Parallel()

	before := []*Skill{{Name: "b"}, {Name: "a"}}
	after := []*Skill{{Name: "c"}, {Name: "a"}, {Name: "d"}}

	diff := diffActive(before, after)
	require.Equal(t, 2, diff.Before)
	require.Equal(t, 3, diff.After)
	require.Equal(t, []string{"c", "d"}, diff.Added)
	require.Equal(t, []string{"b"}, diff.Removed)
}
