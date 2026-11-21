package containermcp

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// NOTE: Gives every container of a group its own state so ordering, health and exit codes can be scripted.
type groupEngine struct {
	*fakeEngine

	gmu       sync.Mutex
	events    []string
	names     map[string]string
	specs     map[string]containerSpec
	exits     map[string]chan exitResult
	health    map[string][]string
	autoExit  map[string]exitResult
	lostExit  map[string]exitResult
	createErr map[string]error

	networkLabels map[string]string
	listedC       []listedContainer
	listedN       []listedNetwork
	removedIDs    []string
	removedNets   []string
}

func newGroupEngine() *groupEngine {
	return &groupEngine{
		fakeEngine: newFakeEngine(),
		names:      map[string]string{},
		specs:      map[string]containerSpec{},
		exits:      map[string]chan exitResult{},
		health:     map[string][]string{},
		autoExit:   map[string]exitResult{},
		lostExit:   map[string]exitResult{},
		createErr:  map[string]error{},
	}
}

func (g *groupEngine) event(e string) {
	g.gmu.Lock()
	defer g.gmu.Unlock()
	g.events = append(g.events, e)
}

func (g *groupEngine) eventList() []string {
	g.gmu.Lock()
	defer g.gmu.Unlock()
	return slices.Clone(g.events)
}

func (g *groupEngine) Create(_ context.Context, spec containerSpec) (string, error) {
	name := spec.Service.Name
	g.event("create:" + name)
	g.gmu.Lock()
	defer g.gmu.Unlock()
	if err := g.createErr[name]; err != nil {
		return "", err
	}
	id := "id-" + name
	g.names[id] = name
	g.specs[name] = spec
	return id, nil
}

func (g *groupEngine) Attach(ctx context.Context, id string) (attachment, error) {
	g.event("attach:" + g.name(id))
	return g.fakeEngine.Attach(ctx, id)
}

func (g *groupEngine) Start(_ context.Context, id string) error {
	name := g.name(id)
	g.event("start:" + name)
	g.gmu.Lock()
	res, ok := g.autoExit[name]
	g.gmu.Unlock()
	if ok {
		g.exitChan(id) <- res
	}
	return nil
}

func (g *groupEngine) Wait(_ context.Context, id string, removed bool) <-chan exitResult {
	if removed {
		return g.exit
	}
	return g.exitChan(id)
}

func (g *groupEngine) exitChan(id string) chan exitResult {
	g.gmu.Lock()
	defer g.gmu.Unlock()
	ch, ok := g.exits[id]
	if !ok {
		ch = make(chan exitResult, 1)
		g.exits[id] = ch
	}
	return ch
}

func (g *groupEngine) name(id string) string {
	g.gmu.Lock()
	defer g.gmu.Unlock()
	return g.names[id]
}

func (g *groupEngine) Inspect(_ context.Context, id string) (containerState, error) {
	name := g.name(id)
	g.gmu.Lock()
	defer g.gmu.Unlock()
	seq := g.health[name]
	status := ""
	if len(seq) > 0 {
		status = seq[0]
		if len(seq) > 1 {
			g.health[name] = seq[1:]
		}
	}
	if res, ok := g.autoExit[name]; ok {
		return containerState{Status: "exited", ExitCode: int(res.Code)}, nil
	}
	if res, ok := g.lostExit[name]; ok {
		return containerState{Status: "exited", ExitCode: int(res.Code)}, nil
	}
	return containerState{Status: "running", Health: status}, nil
}

func (g *groupEngine) Stop(_ context.Context, id string) error {
	g.event("stop:" + g.name(id))
	return nil
}

func (g *groupEngine) Kill(_ context.Context, id string) error {
	g.event("kill:" + g.name(id))
	return nil
}

func (g *groupEngine) Remove(_ context.Context, id string) error {
	g.event("remove:" + g.name(id))
	g.gmu.Lock()
	g.removedIDs = append(g.removedIDs, id)
	g.gmu.Unlock()
	return nil
}

func (g *groupEngine) NetworkCreate(_ context.Context, name string, labels map[string]string) (string, error) {
	g.event("network-create")
	g.gmu.Lock()
	g.networkLabels = labels
	g.specs["<network>"] = containerSpec{Name: name}
	g.gmu.Unlock()
	return "net-id", nil
}

func (g *groupEngine) NetworkRemove(_ context.Context, id string) error {
	g.event("network-remove:" + id)
	g.gmu.Lock()
	g.removedNets = append(g.removedNets, id)
	g.gmu.Unlock()
	return nil
}

func (g *groupEngine) ListContainers(context.Context, string) ([]listedContainer, error) {
	return g.listedC, nil
}

func (g *groupEngine) ListNetworks(context.Context, string) ([]listedNetwork, error) {
	return g.listedN, nil
}

func stackGroup() Group {
	return Group{
		Server: "stack",
		Deps: []Service{
			{Name: "db", Image: "db", Healthcheck: &Healthcheck{Test: []string{"CMD", "true"}}},
			{Name: "cache", Image: "cache"},
		},
		Main: Service{Name: "app", Image: "app", DependsOn: map[string]Condition{
			"db":    ConditionHealthy,
			"cache": ConditionStarted,
		}},
	}
}

func fastHealthPoll(t *testing.T) {
	t.Helper()
	old := healthPoll
	healthPoll = time.Millisecond
	t.Cleanup(func() { healthPoll = old })
}

// Not parallel: it changes the health poll interval.
func TestStart_GroupStartsDependenciesInOrderAndTearsThemDown(t *testing.T) {
	fastHealthPoll(t)
	eng := newGroupEngine()
	eng.health["db"] = []string{"starting", "starting", "healthy"}

	s, err := start(t.Context(), eng, stackGroup())
	require.NoError(t, err)

	require.Equal(t, []string{
		"network-create",
		"create:app", "create:db", "start:db", "create:cache", "start:cache",
		"attach:app", "start:app",
	}, eng.eventList())

	networkName := eng.specs["<network>"].Name
	require.True(t, strings.HasPrefix(networkName, "angela-mcp-stack-"), networkName)
	group := eng.specs["app"].Labels[LabelGroup]
	require.NotEmpty(t, group)
	require.Equal(t, map[string]string{LabelServer: "stack", LabelGroup: group}, eng.networkLabels)
	for _, name := range []string{"app", "db", "cache"} {
		spec := eng.specs[name]
		require.Equal(t, networkName, spec.Network, name)
		require.Empty(t, spec.NetworkMode, name)
		require.Equal(t, group, spec.Labels[LabelGroup], name)
		require.Equal(t, "stack", spec.Labels[LabelServer], name)
	}
	require.Equal(t, roleMain, eng.specs["app"].Labels[LabelRole])
	require.True(t, eng.specs["app"].Interactive)
	require.True(t, eng.specs["app"].AutoRemove)
	for _, name := range []string{"db", "cache"} {
		require.Equal(t, roleDep, eng.specs[name].Labels[LabelRole], name)
		require.False(t, eng.specs[name].Interactive, name)
		require.False(t, eng.specs[name].AutoRemove, name)
	}

	before := len(eng.eventList())
	require.NoError(t, s.Close())
	require.Equal(t, []string{
		"remove:app",
		"stop:cache", "remove:cache",
		"stop:db", "remove:db",
		"network-remove:net-id",
	}, eng.eventList()[before:], "the main container goes first, then dependencies in reverse start order, then the network")
	require.NotContains(t, eng.eventList(), "kill:app", "a main container that exits when stdin closes is not killed")
	require.Contains(t, eng.callList(), "close")
}

// Not parallel: see above.
func TestStart_GroupFailureRemovesEverythingItCreated(t *testing.T) {
	fastHealthPoll(t)

	tests := []struct {
		name    string
		prepare func(*groupEngine)
		want    string
	}{
		{"unhealthy dependency", func(e *groupEngine) { e.health["db"] = []string{"starting", "unhealthy"} }, `service "app" waits for "db": it became unhealthy`},
		{"no healthcheck", func(e *groupEngine) { e.health["db"] = []string{""} }, "it has no healthcheck"},
		{"dependency create fails", func(e *groupEngine) { e.createErr["cache"] = errors.New("no space") }, `create service "cache": no space`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			eng := newGroupEngine()
			tc.prepare(eng)

			s, err := start(t.Context(), eng, stackGroup())
			require.Nil(t, s)
			require.ErrorContains(t, err, tc.want)

			events := eng.eventList()
			require.Contains(t, events, "remove:app")
			require.Contains(t, events, "remove:db")
			require.Contains(t, events, "network-remove:net-id")
			require.NotContains(t, events, "start:app", "the main container never starts")
			require.Contains(t, eng.callList(), "close", "the engine connection is closed")
		})
	}
}

// Not parallel: see above.
func TestStart_GroupWaitsForCompletedDependency(t *testing.T) {
	fastHealthPoll(t)
	g := Group{
		Server: "stack",
		Deps:   []Service{{Name: "migrate", Image: "m"}},
		Main:   Service{Name: "app", Image: "app", DependsOn: map[string]Condition{"migrate": ConditionCompleted}},
	}

	t.Run("exit zero lets the main container start", func(t *testing.T) {
		eng := newGroupEngine()
		eng.autoExit["migrate"] = exitResult{Code: 0}
		s, err := start(t.Context(), eng, g)
		require.NoError(t, err)
		require.Contains(t, eng.eventList(), "start:app")
		require.NoError(t, s.Close())
	})
	t.Run("a non zero exit stops the startup", func(t *testing.T) {
		eng := newGroupEngine()
		eng.autoExit["migrate"] = exitResult{Code: 3}
		_, err := start(t.Context(), eng, g)
		require.ErrorContains(t, err, "it exited with code 3")
		require.NotContains(t, eng.eventList(), "start:app")
	})
	t.Run("an exit the engine never reports is found by polling", func(t *testing.T) {
		eng := newGroupEngine()
		eng.lostExit["migrate"] = exitResult{Code: 0}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		s, err := start(ctx, eng, g)
		require.NoError(t, err)
		require.Contains(t, eng.eventList(), "start:app")
		require.NoError(t, s.Close())
	})
	t.Run("a lost non zero exit still stops the startup", func(t *testing.T) {
		eng := newGroupEngine()
		eng.lostExit["migrate"] = exitResult{Code: 4}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		_, err := start(ctx, eng, g)
		require.ErrorContains(t, err, "it exited with code 4")
		require.NotContains(t, eng.eventList(), "start:app")
	})
}

// Not parallel: see above.
func TestStart_GroupHealthWaitEndsWithTheContext(t *testing.T) {
	fastHealthPoll(t)
	eng := newGroupEngine()
	eng.health["db"] = []string{"starting"}

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := start(ctx, eng, stackGroup())

	require.ErrorContains(t, err, "did not become healthy")
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, eng.eventList(), "remove:db", "cleanup still runs after the startup context ended")
}

func TestStart_GroupWithoutNetworkNeedWhenEveryServiceHasAMode(t *testing.T) {
	t.Parallel()

	eng := newGroupEngine()
	g := Group{
		Server: "stack",
		Deps:   []Service{{Name: "db", Image: "db", NetworkMode: "host"}},
		Main:   Service{Name: "app", Image: "app", NetworkMode: "none", DependsOn: map[string]Condition{"db": ConditionStarted}},
	}
	s, err := start(t.Context(), eng, g)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	require.NotContains(t, eng.eventList(), "network-create")
	require.Equal(t, "host", eng.specs["db"].NetworkMode)
	require.Equal(t, "none", eng.specs["app"].NetworkMode)
	require.Empty(t, eng.specs["db"].Network)
}

func TestStart_GroupSweepsOnlyGroupsWhoseMainContainerIsGone(t *testing.T) {
	t.Parallel()

	old := time.Now().Add(-time.Hour)
	label := func(group, role string) map[string]string {
		return map[string]string{LabelServer: "stack", LabelGroup: group, LabelRole: role}
	}
	eng := newGroupEngine()
	eng.listedC = []listedContainer{
		{ID: "dead-dep", Labels: label("dead", roleDep), State: "running"},
		{ID: "live-main", Labels: label("live", roleMain), State: "running"},
		{ID: "live-dep", Labels: label("live", roleDep), State: "running"},
		{ID: "young-main", Labels: label("young", roleMain), State: "created", Created: time.Now()},
		{ID: "young-dep", Labels: label("young", roleDep), State: "running"},
		{ID: "stale-main", Labels: label("stale", roleMain), State: "created", Created: old},
		{ID: "stale-dep", Labels: label("stale", roleDep), State: "running"},
		{ID: "exited-main", Labels: label("exited", roleMain), State: "exited"},
		{ID: "unlabelled", Labels: map[string]string{LabelServer: "stack"}, State: "running"},
	}
	eng.listedN = []listedNetwork{
		{ID: "net-dead", Labels: label("dead", "")},
		{ID: "net-live", Labels: label("live", "")},
		{ID: "net-stale", Labels: label("stale", "")},
	}

	g := stackGroup()
	g.Main.DependsOn["db"] = ConditionStarted
	s, err := start(t.Context(), eng, g)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// NOTE: This is read before Close, so only the sweep has removed anything.
	eng.gmu.Lock()
	removed := slices.Clone(eng.removedIDs)
	nets := slices.Clone(eng.removedNets)
	eng.gmu.Unlock()
	slices.Sort(removed)
	slices.Sort(nets)
	require.Equal(t, []string{"dead-dep", "exited-main", "stale-dep", "stale-main"}, removed)
	require.Equal(t, []string{"net-dead", "net-stale"}, nets)
}

func TestGroupValidate_Order(t *testing.T) {
	t.Parallel()

	svc := func(name string, deps ...string) Service {
		s := Service{Name: name, Image: "img"}
		if len(deps) > 0 {
			s.DependsOn = map[string]Condition{}
			for _, d := range deps {
				s.DependsOn[d] = ConditionStarted
			}
		}
		return s
	}

	require.NoError(t, Group{Server: "s", Deps: []Service{svc("a"), svc("b", "a")}, Main: svc("m", "b")}.Validate())
	require.ErrorContains(t,
		Group{Server: "s", Deps: []Service{svc("b", "a"), svc("a")}, Main: svc("m", "b")}.Validate(),
		"does not start before it")
	require.ErrorContains(t,
		Group{Server: "s", Deps: []Service{svc("a")}, Main: svc("m", "ghost")}.Validate(),
		`depends on "ghost"`)
	require.ErrorContains(t,
		Group{Server: "s", Deps: []Service{svc("a"), svc("a")}, Main: svc("m")}.Validate(),
		"appears twice")
	require.ErrorContains(t,
		Group{Server: "s", Deps: []Service{svc("m")}, Main: svc("m")}.Validate(),
		"appears twice")

	bad := svc("m", "a")
	bad.DependsOn["a"] = "sometimes"
	require.ErrorContains(t, Group{Server: "s", Deps: []Service{svc("a")}, Main: bad}.Validate(), "unknown depends_on condition")

	badHealth := svc("a")
	badHealth.Healthcheck = &Healthcheck{Test: []string{"bogus"}}
	require.ErrorContains(t, Group{Server: "s", Deps: []Service{badHealth}, Main: svc("m")}.Validate(), "healthcheck test")

	badMode := svc("m")
	badMode.NetworkMode = "container:x"
	require.ErrorContains(t, Group{Server: "s", Main: badMode}.Validate(), "network mode")
}

func TestSafeName(t *testing.T) {
	t.Parallel()

	require.Equal(t, "my-server_1.x", safeName("my-server_1.x"))
	require.Equal(t, "a-b-c", safeName("a b/c"))
}
