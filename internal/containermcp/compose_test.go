package containermcp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
      resources:
        limits:
          cpus: "1"
    build: .
`, "app")
	require.NoError(t, err)
	require.Equal(t, "example/app", g.Main.Image)
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
		{"security_opt", "security_opt: [\"seccomp:unconfined\"]", []string{`"security_opt"`}},
		{"pid host", "pid: host", []string{`"pid"`}},
		{"ipc host", "ipc: host", []string{`"ipc"`}},
		{"sysctls", "sysctls: {net.core.somaxconn: 1024}", []string{`"sysctls"`}},
		{"volumes_from", "volumes_from: [other]", []string{`"volumes_from"`}},
		{"unknown field", "read_only: true", []string{`"read_only"`, "not supported"}},
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
	_, _, err := loadCompose(t, "services:\n  app:\n    image: example/app\n    volumes: [\""+runtimeDir+":/run/host\"]\n", "app")
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
	t.Run("a profile service is refused", func(t *testing.T) {
		_, _, err := loadCompose(t, "services:\n  app:\n    image: example/app\n    profiles: [debug]\n", "app")
		require.ErrorContains(t, err, "profile")
	})
	t.Run("a dependency cycle is an error", func(t *testing.T) {
		_, _, err := loadCompose(t, "services:\n  a:\n    image: x\n    depends_on: [b]\n  b:\n    image: x\n    depends_on: [a]\n", "a")
		require.Error(t, err)
	})
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
