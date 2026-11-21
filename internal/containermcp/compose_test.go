package containermcp

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// NOTE: Compose files interpolate from the process environment, so callers cannot run in parallel when they set variables.
func loadCompose(t *testing.T, yaml, service string) (Group, string, error) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "compose.yaml"), yaml)
	g, err := FromCompose(t.Context(), "srv", "compose.yaml", service, dir)
	return g, dir, err
}

func TestFromCompose_SingleService(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "data"), 0o755))
	writeFile(t, filepath.Join(dir, "compose.yaml"), `
services:
  app:
    image: example/app:1
    command: ["serve", "--stdio"]
    entrypoint: ["/bin/app"]
    working_dir: /srv
    user: "1000:1000"
    environment:
      MODE: test
      EMPTY: ""
    labels:
      team: infra
    volumes:
      - ./data:/data:ro
      - cache:/cache
      - type: tmpfs
        target: /tmp
    networks:
      default:
        aliases: [web]
volumes:
  cache: {}
`)

	g, err := FromCompose(t.Context(), "srv", "compose.yaml", "app", dir)
	require.NoError(t, err)

	require.Equal(t, "srv", g.Server)
	require.Empty(t, g.Deps)
	app := g.Main
	require.Equal(t, "app", app.Name)
	require.Equal(t, "example/app:1", app.Image)
	require.Equal(t, []string{"serve", "--stdio"}, app.Cmd)
	require.Equal(t, []string{"/bin/app"}, app.Entrypoint)
	require.Equal(t, "/srv", app.WorkingDir)
	require.Equal(t, "1000:1000", app.User)
	require.Equal(t, map[string]string{"MODE": "test", "EMPTY": ""}, app.Env)
	require.Equal(t, map[string]string{"team": "infra"}, app.Labels)
	require.Equal(t, []string{"web"}, app.Aliases)
	require.Equal(t, []Mount{
		{Type: MountBind, Source: filepath.Join(dir, "data"), Target: "/data", ReadOnly: true},
		{Type: MountVolume, Source: "angela-srv_cache", Target: "/cache"},
		{Type: MountTmpfs, Target: "/tmp"},
	}, app.Mounts)
}

func TestFromCompose_DependenciesComeFirstWithConditions(t *testing.T) {
	g, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    depends_on:
      migrate:
        condition: service_completed_successfully
      db:
        condition: service_healthy
      cache:
        condition: service_started
  migrate:
    image: example/migrate
    depends_on:
      db:
        condition: service_healthy
  db:
    image: example/db
    healthcheck:
      test: ["CMD-SHELL", "pg_isready"]
      interval: 2s
      timeout: 1s
      start_period: 5s
      retries: 4
  cache:
    image: example/cache
  unrelated:
    image: example/unrelated
`, "app")
	require.NoError(t, err)

	require.Equal(t, "app", g.Main.Name)
	names := make([]string, 0, len(g.Deps))
	for _, d := range g.Deps {
		names = append(names, d.Name)
	}
	require.ElementsMatch(t, []string{"db", "migrate", "cache"}, names, "services the target does not need are left out")
	index := func(name string) int {
		for i, d := range g.Deps {
			if d.Name == name {
				return i
			}
		}
		return -1
	}
	require.Less(t, index("db"), index("migrate"), "a dependency starts before its dependent")

	require.Equal(t, map[string]Condition{
		"migrate": ConditionCompleted,
		"db":      ConditionHealthy,
		"cache":   ConditionStarted,
	}, g.Main.DependsOn)
	db := g.Deps[index("db")]
	require.Equal(t, &Healthcheck{
		Test:        []string{"CMD-SHELL", "pg_isready"},
		Interval:    2 * time.Second,
		Timeout:     time.Second,
		StartPeriod: 5 * time.Second,
		Retries:     4,
	}, db.Healthcheck)
}

func TestFromCompose_InterpolationAndEnvFiles(t *testing.T) {
	t.Setenv("ANGELA_TEST_TAG", "2.5")
	t.Setenv("ANGELA_TEST_PASS", "from-host")
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".env"), "FROM_DOTENV=dot\n")
	writeFile(t, filepath.Join(dir, "app.env"), "FILE_VAR=file\n")
	writeFile(t, filepath.Join(dir, "compose.yaml"), `
services:
  app:
    image: example/app:${ANGELA_TEST_TAG}
    env_file: app.env
    environment:
      DOT: ${FROM_DOTENV}
      PASSED: ${ANGELA_TEST_PASS}
      UNSET_PASS:
      DEFAULTED: ${ANGELA_TEST_MISSING:-fallback}
`)

	g, err := FromCompose(t.Context(), "srv", "compose.yaml", "app", dir)
	require.NoError(t, err)

	require.Equal(t, "example/app:2.5", g.Main.Image)
	require.Equal(t, "dot", g.Main.Env["DOT"])
	require.Equal(t, "from-host", g.Main.Env["PASSED"])
	require.Equal(t, "fallback", g.Main.Env["DEFAULTED"])
	require.Equal(t, "file", g.Main.Env["FILE_VAR"], "env_file values reach the container")
	require.NotContains(t, g.Main.Env, "UNSET_PASS", "a variable with no value on the host stays unset")
}

func TestFromCompose_LoaderWarningsStayOffStderr(t *testing.T) {
	_, _, err := loadCompose(t, "services:\n  app:\n    image: example/app:${ANGELA_TEST_UNSET_TAG}\n", "app")
	require.NoError(t, err)
	require.IsType(t, composeLogWriter{}, logrus.StandardLogger().Out, "the loader's logger must not write to the terminal")
}

func TestFromCompose_Include(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "base", "db.yaml"), `
services:
  db:
    image: example/db
`)
	writeFile(t, filepath.Join(dir, "compose.yaml"), `
include:
  - base/db.yaml
services:
  app:
    image: example/app
    depends_on: [db]
`)

	g, err := FromCompose(t.Context(), "srv", "compose.yaml", "app", dir)
	require.NoError(t, err)
	require.Len(t, g.Deps, 1)
	require.Equal(t, "db", g.Deps[0].Name)
}

func TestFromCompose_RemoteIncludeIsRefused(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "compose.yaml"), `
include:
  - path: https://127.0.0.1:1/compose.yaml
services:
  app:
    image: example/app
`)

	_, err := FromCompose(t.Context(), "srv", "compose.yaml", "app", dir)
	var pathErr *os.PathError
	require.ErrorAs(t, err, &pathErr, "the URL is read as a local path and never fetched")
	require.Equal(t, "open", pathErr.Op)
	require.Contains(t, pathErr.Path, dir)
}

func TestFromCompose_Extends(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "base.yaml"), `
services:
  base:
    image: example/base
    environment:
      FROM_BASE: "yes"
`)
	writeFile(t, filepath.Join(dir, "compose.yaml"), `
services:
  app:
    extends:
      file: base.yaml
      service: base
    environment:
      OWN: "yes"
`)

	g, err := FromCompose(t.Context(), "srv", "compose.yaml", "app", dir)
	require.NoError(t, err)
	require.Equal(t, "example/base", g.Main.Image)
	require.Equal(t, map[string]string{"FROM_BASE": "yes", "OWN": "yes"}, g.Main.Env)
}

func TestFromCompose_IgnoredFieldsDoNotFail(t *testing.T) {
	g, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    restart: always
    container_name: fixed
    ports: ["8080:80"]
    expose: ["80"]
    stdin_open: true
    logging:
      driver: json-file
    deploy:
      restart_policy:
        condition: any
      update_config:
        parallelism: 2
    build: .
`, "app")
	require.NoError(t, err)
	require.Equal(t, "example/app", g.Main.Image)
	require.Equal(t, Limits{}, g.Main.Limits, "swarm scheduling settings in deploy change nothing")
}

func TestFromCompose_DeployResources(t *testing.T) {
	g, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    deploy:
      resources:
        limits:
          cpus: "0.5"
          memory: 256m
          pids: 100
        reservations:
          memory: 128m
`, "app")
	require.NoError(t, err)
	require.Equal(t, Limits{
		Memory: 256 << 20, MemoryReservation: 128 << 20,
		NanoCPUs: 500_000_000, PidsLimit: 100,
	}, g.Main.Limits)
}

func TestFromCompose_DeployResourcesAgreeingWithTopLevelFields(t *testing.T) {
	g, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    mem_limit: 256m
    pids_limit: 100
    deploy:
      resources:
        limits:
          memory: 256m
          cpus: "1"
          pids: 100
`, "app")
	require.NoError(t, err)
	require.Equal(t, Limits{Memory: 256 << 20, NanoCPUs: 1_000_000_000, PidsLimit: 100}, g.Main.Limits)
}

func TestFromCompose_DeployResourcesCannotContradictTopLevelFields(t *testing.T) {
	_, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    mem_limit: 256m
    deploy:
      resources:
        limits:
          memory: 512m
`, "app")
	require.ErrorContains(t, err, "can't set distinct values on 'mem_limit' and 'deploy.resources.limits.memory'")
}

func TestFromCompose_DeployResourcesAreRefusedWhenTheyWouldWidenOrDoNothing(t *testing.T) {
	tests := []struct {
		name      string
		resources string
		want      []string
	}{
		{"gpu device", `
        reservations:
          devices:
            - capabilities: [gpu]
`, []string{"deploy.resources.reservations.devices", "exposes host devices"}},
		{"cpu reservation", `
        reservations:
          cpus: "0.5"
`, []string{"deploy.resources.reservations.cpus", "not supported"}},
		{"generic resource", `
        reservations:
          generic_resources:
            - discrete_resource_spec:
                kind: ssd
                value: 1
`, []string{"deploy.resources.reservations.generic_resources", "not supported"}},
		{"negative pids", `
        limits:
          pids: -1
`, []string{"pids limit must not be negative"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := loadCompose(t, "services:\n  app:\n    image: example/app\n    deploy:\n      resources:"+tc.resources, "app")
			require.Error(t, err)
			for _, want := range tc.want {
				require.ErrorContains(t, err, want)
			}
			require.ErrorContains(t, err, `"app"`, "the message names the service")
		})
	}
}

func TestFromCompose_MemSwappiness(t *testing.T) {
	tests := []struct {
		name  string
		line  string
		want  *int64
		fails string
	}{
		{name: "a percentage", line: "mem_swappiness: 60", want: new(int64(60))},
		{name: "the upper bound", line: "mem_swappiness: 100", want: new(int64(100))},
		{name: "an explicit zero", line: "mem_swappiness: 0", want: new(int64(0))},
		{name: "zero as a string", line: `mem_swappiness: "0"`, want: new(int64(0))},
		{name: "the engine default", line: "mem_swappiness: -1"},
		{name: "not set", line: "mem_limit: 64m"},
		{name: "above a hundred", line: "mem_swappiness: 101", fails: "mem_swappiness must be a percentage"},
		{name: "below the default marker", line: "mem_swappiness: -2", fails: "mem_swappiness must be a percentage"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g, _, err := loadCompose(t, "services:\n  app:\n    image: example/app\n    "+tc.line+"\n", "app")
			if tc.fails != "" {
				require.ErrorContains(t, err, tc.fails)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, g.Main.Limits.MemorySwappiness)
		})
	}
}

func TestSwarmSettings(t *testing.T) {
	t.Parallel()

	var d types.DeployConfig
	require.Empty(t, swarmSettings(d), "an empty deploy sets nothing")

	d.Resources.Limits = &types.Resource{MemoryBytes: 1 << 20}
	require.Empty(t, swarmSettings(d), "resources are applied, not ignored")

	d.Placement.Constraints = []string{"node.role==manager"}
	d.RestartPolicy = &types.RestartPolicy{Condition: "any"}
	require.Equal(t, []string{"placement", "restart_policy"}, swarmSettings(d))
}

func TestFromCompose_ExplicitZeroSwappinessSurvivesInclude(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "base", "db.yaml"), `
services:
  db:
    image: example/db
    mem_swappiness: 0
`)
	writeFile(t, filepath.Join(dir, "compose.yaml"), `
include:
  - base/db.yaml
services:
  app:
    image: example/app
    depends_on: [db]
`)

	g, err := FromCompose(t.Context(), "srv", "compose.yaml", "app", dir)
	require.NoError(t, err)
	require.Len(t, g.Deps, 1)
	require.Equal(t, new(int64(0)), g.Deps[0].Limits.MemorySwappiness, "an included service keeps an explicit zero")
	require.Nil(t, g.Main.Limits.MemorySwappiness)
}

func TestFromCompose_RejectedFields(t *testing.T) {
	tests := []struct {
		name    string
		service string
		want    []string
	}{
		{"privileged", "privileged: true", []string{`"privileged"`, "full access"}},
		{"cap_add", "cap_add: [SYS_ADMIN]", []string{`"cap_add"`}},
		{"devices", "devices: [\"/dev/fuse:/dev/fuse\"]", []string{`"devices"`}},
		{"security_opt", "security_opt: [\"seccomp:unconfined\"]", []string{`"security_opt"`, "only no-new-privileges"}},
		{"security_opt with equals", "security_opt: [\"apparmor=unconfined\"]", []string{`"security_opt"`, "only no-new-privileges"}},
		{"security_opt label", "security_opt: [\"label=disable\"]", []string{`"security_opt"`}},
		{"security_opt value", "security_opt: [\"no-new-privileges:maybe\"]", []string{`"security_opt"`, "true or false"}},
		{"pid host", "pid: host", []string{`"pid"`}},
		{"ipc host", "ipc: host", []string{`"ipc"`}},
		{"sysctls", "sysctls: {net.core.somaxconn: 1024}", []string{`"sysctls"`}},
		{"volumes_from", "volumes_from: [other]", []string{`"volumes_from"`}},
		{"unknown field", "ulimits: {nofile: 1024}", []string{`"ulimits"`, "not supported"}},
		{"group_add", "group_add: [audio]", []string{`"group_add"`, "not supported"}},
		{"negative pids limit", "pids_limit: -1", []string{"pids limit must not be negative"}},
		{"tmpfs suid", "tmpfs: [\"/run:suid\"]", []string{"widens"}},
		{"bad dns", "dns: [resolver.local]", []string{"not an IP address"}},
		{"bad cap_drop", "cap_drop: [\"NET RAW\"]", []string{"cap_drop"}},
		{"network_mode service", "network_mode: service:other", []string{"network_mode"}},
		{"main tty", "tty: true", []string{"tty"}},
		{"anonymous volume", "volumes: [\"/data\"]", []string{"anonymous"}},
		{"unsupported condition policy", "pull_policy: daily", []string{"pull_policy"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := loadCompose(t, "services:\n  other:\n    image: example/other\n  app:\n    image: example/app\n    "+tc.service+"\n", "app")
			require.Error(t, err)
			for _, want := range tc.want {
				require.ErrorContains(t, err, want)
			}
			require.ErrorContains(t, err, `"app"`, "the message names the service")
		})
	}
}

func TestFromCompose_RejectsEveryProblemInOneService(t *testing.T) {
	_, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    privileged: true
    cap_add: [NET_ADMIN]
`, "app")
	require.ErrorContains(t, err, `"cap_add"`)
	require.ErrorContains(t, err, `"privileged"`)
}

func TestFromCompose_DependencyIsCheckedToo(t *testing.T) {
	_, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    depends_on: [db]
  db:
    image: example/db
    privileged: true
`, "app")
	require.ErrorContains(t, err, `compose service "db"`)
	require.ErrorContains(t, err, `"privileged"`)
}

func TestFromCompose_HardeningLimitsAndIdentity(t *testing.T) {
	g, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    cap_drop: [ALL, NET_RAW]
    read_only: true
    security_opt:
      - no-new-privileges:true
    tmpfs:
      - /run
      - /scratch:size=64m,noexec
    volumes:
      - type: tmpfs
        target: /cache
        tmpfs:
          size: 1048576
          mode: 0o1777
    mem_limit: 256m
    mem_reservation: 128m
    memswap_limit: 512m
    cpus: 0.5
    cpu_shares: 512
    cpu_period: 100000
    cpu_quota: 50000
    cpuset: "0-1"
    pids_limit: 100
    hostname: mcp-app
    domainname: corp.test
    dns: [1.1.1.1, "2606:4700:4700::1111"]
    dns_search: [corp.test]
    dns_opt: ["ndots:2"]
    stop_signal: SIGINT
`, "app")
	require.NoError(t, err)

	app := g.Main
	require.Equal(t, []string{"ALL", "NET_RAW"}, app.CapDrop)
	require.True(t, app.ReadOnly)
	require.True(t, app.NoNewPrivileges)
	require.Equal(t, []Mount{
		{Type: MountTmpfs, Target: "/cache", Options: "size=1048576,mode=1777"},
		{Type: MountTmpfs, Target: "/run"},
		{Type: MountTmpfs, Target: "/scratch", Options: "size=64m,noexec"},
	}, app.Mounts)
	require.Equal(t, Limits{
		Memory: 256 << 20, MemoryReservation: 128 << 20, MemorySwap: 512 << 20,
		NanoCPUs: 500_000_000, CPUShares: 512, CPUPeriod: 100000, CPUQuota: 50000,
		CPUSet: "0-1", PidsLimit: 100,
	}, app.Limits)
	require.Equal(t, "mcp-app", app.Hostname)
	require.Equal(t, "corp.test", app.Domainname)
	require.Equal(t, []string{"1.1.1.1", "2606:4700:4700::1111"}, app.DNS)
	require.Equal(t, []string{"corp.test"}, app.DNSSearch)
	require.Equal(t, []string{"ndots:2"}, app.DNSOptions)
	require.Equal(t, "SIGINT", app.StopSignal)
}

func TestFromCompose_ReadOnlyTmpfsVolume(t *testing.T) {
	g, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    volumes:
      - type: tmpfs
        target: /ro
        read_only: true
`, "app")
	require.NoError(t, err)
	require.Equal(t, []Mount{{Type: MountTmpfs, Target: "/ro", ReadOnly: true, Options: "ro"}}, g.Main.Mounts)
}

func TestFromCompose_SecurityOptForms(t *testing.T) {
	tests := []struct {
		name string
		opts string
		want bool
	}{
		{"colon form", `["no-new-privileges:true"]`, true},
		{"equals form", `["no-new-privileges=true"]`, true},
		{"bare", `["no-new-privileges"]`, true},
		{"explicit false", `["no-new-privileges:false"]`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g, _, err := loadCompose(t, "services:\n  app:\n    image: example/app\n    security_opt: "+tc.opts+"\n", "app")
			require.NoError(t, err)
			require.Equal(t, tc.want, g.Main.NoNewPrivileges)
		})
	}
}

func TestFromCompose_NegativeMemoryLimitIsRefusedByTheLoader(t *testing.T) {
	_, _, err := loadCompose(t, "services:\n  app:\n    image: example/app\n    mem_limit: -1\n", "app")
	require.ErrorContains(t, err, "services.app.mem_limit")
	require.ErrorContains(t, err, "must not be negative")
}

func TestFromCompose_OptionsDefaultToOff(t *testing.T) {
	g, _, err := loadCompose(t, "services:\n  app:\n    image: example/app\n", "app")
	require.NoError(t, err)

	app := g.Main
	require.Empty(t, app.CapDrop)
	require.False(t, app.ReadOnly)
	require.False(t, app.NoNewPrivileges)
	require.Equal(t, Limits{}, app.Limits)
	require.Empty(t, app.Mounts)
	require.Empty(t, app.DNS)
}

func TestFromCompose_BuildNeedsAnImage(t *testing.T) {
	_, _, err := loadCompose(t, "services:\n  app:\n    build: .\n", "app")
	require.Error(t, err)
}

func TestFromCompose_DepTtyIsAllowed(t *testing.T) {
	g, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    depends_on: [shell]
  shell:
    image: example/shell
    tty: true
`, "app")
	require.NoError(t, err)
	require.True(t, g.Deps[0].Tty)
	require.False(t, g.Main.Tty)
}

func TestFromCompose_NetworkMode(t *testing.T) {
	g, _, err := loadCompose(t, "services:\n  app:\n    image: example/app\n    network_mode: none\n", "app")
	require.NoError(t, err)
	require.Equal(t, "none", g.Main.NetworkMode)
}

func TestFromCompose_PullPolicy(t *testing.T) {
	g, _, err := loadCompose(t, "services:\n  app:\n    image: example/app\n    pull_policy: never\n", "app")
	require.NoError(t, err)
	require.Equal(t, PullNever, g.Main.Pull)
}

func TestFromCompose_VolumeDriverOptionsAreRejected(t *testing.T) {
	_, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    volumes: ["vol:/data"]
volumes:
  vol:
    driver_opts:
      type: none
      o: bind
      device: /
`, "app")
	require.ErrorContains(t, err, "driver")
}

func TestFromCompose_BindSourceMustNotExposeTheEngineSocket(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	_, _, err := loadCompose(t, "services:\n  app:\n    image: example/app\n    volumes:\n      - type: bind\n        source: "+strconv.Quote(runtimeDir)+"\n        target: /run/host\n", "app")
	require.ErrorContains(t, err, "container engine socket")
}

func TestFromCompose_MissingBindSource(t *testing.T) {
	_, _, err := loadCompose(t, "services:\n  app:\n    image: example/app\n    volumes: [\"./nope:/data\"]\n", "app")
	require.ErrorContains(t, err, "does not exist")
}

func TestFromCompose_ServiceErrors(t *testing.T) {
	t.Run("unknown service lists the real ones", func(t *testing.T) {
		_, _, err := loadCompose(t, "services:\n  app:\n    image: example/app\n", "web")
		require.ErrorContains(t, err, `no service "web"`)
		require.ErrorContains(t, err, "app")
	})
	t.Run("a dependency cycle is an error", func(t *testing.T) {
		_, _, err := loadCompose(t, "services:\n  a:\n    image: x\n    depends_on: [b]\n  b:\n    image: x\n    depends_on: [a]\n", "a")
		require.Error(t, err)
	})
}

func TestFromCompose_NamingAServiceEnablesItsProfiles(t *testing.T) {
	g, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    profiles: [stdio]
    depends_on:
      db:
        condition: service_started
  db:
    image: example/db
  other:
    image: example/other
    profiles: [debug]
`, "app")
	require.NoError(t, err)
	require.Equal(t, "app", g.Main.Name)
	require.Len(t, g.Deps, 1)
	require.Equal(t, "db", g.Deps[0].Name)
}

func TestFromCompose_DependencyOnAnotherProfileIsAnError(t *testing.T) {
	_, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    profiles: [stdio]
    depends_on: [tool]
  tool:
    image: example/tool
    profiles: [debug]
`, "app")
	require.Error(t, err)
	require.ErrorContains(t, err, `"app" depends on "tool"`)
	require.ErrorContains(t, err, "profile")
}

func TestFromCompose_OptionalDependencyInAnotherProfileIsDropped(t *testing.T) {
	g, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    depends_on:
      tool:
        condition: service_started
        required: false
  tool:
    image: example/tool
    profiles: [debug]
`, "app")
	require.NoError(t, err)
	require.Empty(t, g.Deps)
	require.Empty(t, g.Main.DependsOn)
}

func TestFromCompose_InitShmSizeAndExtraHosts(t *testing.T) {
	g, _, err := loadCompose(t, `
services:
  app:
    image: example/app
    init: true
    shm_size: 1gb
    extra_hosts:
      - "host.docker.internal:host-gateway"
      - "db.local=10.0.0.5"
      - "db.local:10.0.0.4"
`, "app")
	require.NoError(t, err)
	require.True(t, g.Main.Init)
	require.EqualValues(t, 1<<30, g.Main.ShmSize)
	require.Equal(t, []string{
		"db.local:10.0.0.4",
		"db.local:10.0.0.5",
		"host.docker.internal:host-gateway",
	}, g.Main.ExtraHosts)
}

func TestFromCompose_InitDefaultsToOff(t *testing.T) {
	g, _, err := loadCompose(t, "services:\n  app:\n    image: example/app\n    init: false\n", "app")
	require.NoError(t, err)
	require.False(t, g.Main.Init)
	require.Zero(t, g.Main.ShmSize)
	require.Empty(t, g.Main.ExtraHosts)
}

func TestFromCompose_FileErrors(t *testing.T) {
	dir := t.TempDir()
	_, err := FromCompose(t.Context(), "srv", "missing.yaml", "app", dir)
	require.Error(t, err)

	_, err = FromCompose(t.Context(), "srv", ".", "app", dir)
	require.ErrorContains(t, err, "directory")

	writeFile(t, filepath.Join(dir, "bad.yaml"), "services: [")
	_, err = FromCompose(t.Context(), "srv", "bad.yaml", "app", dir)
	require.Error(t, err)

	_, err = FromCompose(t.Context(), "srv", "rel.yaml", "app", "")
	require.ErrorContains(t, err, "project directory")
}

func TestFromCompose_AbsoluteFilePath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "c.yaml"), "services:\n  app:\n    image: example/app\n")
	g, err := FromCompose(t.Context(), "srv", filepath.Join(dir, "c.yaml"), "app", "")
	require.NoError(t, err)
	require.Equal(t, "example/app", g.Main.Image)
}
