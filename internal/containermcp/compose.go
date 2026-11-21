package containermcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NaturalSelect/angela/internal/home"
	"github.com/compose-spec/compose-go/v2/cli"
	"github.com/compose-spec/compose-go/v2/types"
	"github.com/sirupsen/logrus"
)

var composeSupported = map[string]bool{
	"image": true, "command": true, "entrypoint": true, "environment": true, "env_file": true,
	"volumes": true, "working_dir": true, "user": true, "depends_on": true, "healthcheck": true,
	"labels": true, "networks": true, "network_mode": true, "pull_policy": true, "tty": true,
}

// NOTE: These fields do not apply to a container started for an MCP server, so they only log a warning.
var composeIgnored = map[string]bool{
	"restart": true, "deploy": true, "logging": true, "log_driver": true, "log_opt": true,
	"ports": true, "expose": true, "container_name": true, "stdin_open": true, "attach": true,
	"develop": true, "scale": true, "build": true,
}

// NOTE: Anything not listed here or above is rejected too; these only get a reason in the message.
var composeRejected = map[string]string{
	"privileged":          "it gives the container full access to the host",
	"cap_add":             "it adds capabilities to the container",
	"devices":             "it exposes host devices",
	"device_cgroup_rules": "it exposes host devices",
	"security_opt":        "it changes the container's security profile",
	"pid":                 "it can share the host's process namespace",
	"ipc":                 "it can share the host's IPC namespace",
	"uts":                 "it can share the host's hostname namespace",
	"userns_mode":         "it can share the host's user namespace",
	"cgroup":              "it can share the host's cgroup namespace",
	"cgroup_parent":       "it places the container in a host cgroup",
	"sysctls":             "it changes kernel parameters",
	"volumes_from":        "it mounts another container's volumes",
	"use_api_socket":      "it gives the container the engine API",
}

// NOTE: Loads a compose file and translates the selected service and what it depends on; profiles are never activated.
func FromCompose(ctx context.Context, server, file, service, workDir string) (Group, error) {
	redirectComposeLogs()
	path := home.Long(file)
	if !filepath.IsAbs(path) {
		if workDir == "" {
			return Group{}, fmt.Errorf("compose file %q is relative but the project directory is unknown", file)
		}
		path = filepath.Join(workDir, path)
	}
	path = filepath.Clean(path)
	fi, err := os.Stat(path)
	if err != nil {
		return Group{}, fmt.Errorf("compose file: %w", err)
	}
	if fi.IsDir() {
		return Group{}, fmt.Errorf("compose file %q is a directory", path)
	}

	opts, err := cli.NewProjectOptions(
		[]string{path},
		cli.WithWorkingDirectory(filepath.Dir(path)),
		cli.WithOsEnv,
		cli.WithEnvFiles(),
		cli.WithDotEnv,
		cli.WithInterpolation(true),
		cli.WithResolvedPaths(true),
		cli.WithName(composeProjectName(server)),
	)
	if err != nil {
		return Group{}, fmt.Errorf("compose file %q: %w", path, err)
	}
	project, err := opts.LoadProject(ctx)
	if err != nil {
		return Group{}, fmt.Errorf("compose file %q: %w", path, err)
	}
	if _, ok := project.Services[service]; !ok {
		if _, off := project.DisabledServices[service]; off {
			return Group{}, fmt.Errorf("compose service %q needs a profile, and profiles are not supported", service)
		}
		return Group{}, fmt.Errorf("compose file %q has no service %q (it has %s)", path, service, strings.Join(project.ServiceNames(), ", "))
	}

	var ordered []Service
	err = project.ForEachService([]string{service}, func(name string, svc *types.ServiceConfig) error {
		s, err := translateService(project, name, *svc, name == service)
		if err != nil {
			return err
		}
		ordered = append(ordered, s)
		return nil
	}, types.IncludeDependencies)
	if err != nil {
		return Group{}, err
	}

	// NOTE: A dependency that is not in the group is an optional one the project disabled.
	present := map[string]bool{}
	for _, s := range ordered {
		present[s.Name] = true
	}
	for _, s := range ordered {
		for dep := range s.DependsOn {
			if !present[dep] {
				delete(s.DependsOn, dep)
			}
		}
	}

	last := len(ordered) - 1
	if last < 0 || ordered[last].Name != service {
		return Group{}, fmt.Errorf("compose service %q was not resolved", service)
	}
	g := Group{Server: server, Main: ordered[last], Deps: ordered[:last]}
	if err := g.Validate(); err != nil {
		return Group{}, err
	}
	return g, nil
}

// NOTE: The compose loader logs through logrus to stderr, which would draw over the terminal UI.
var redirectComposeLogs = sync.OnceFunc(func() { logrus.SetOutput(composeLogWriter{}) })

type composeLogWriter struct{}

func (composeLogWriter) Write(p []byte) (int, error) {
	slog.Debug("Compose loader", "message", strings.TrimSpace(string(p)))
	return len(p), nil
}

func composeProjectName(server string) string {
	name := strings.ToLower(safeName(server))
	name = strings.ReplaceAll(name, ".", "-")
	return "angela-" + name
}

func translateService(project *types.Project, name string, svc types.ServiceConfig, isMain bool) (Service, error) {
	if err := checkComposeFields(name, svc); err != nil {
		return Service{}, err
	}
	if svc.Image == "" {
		return Service{}, fmt.Errorf("compose service %q has no image; Angela does not build images", name)
	}
	if isMain && svc.Tty {
		return Service{}, fmt.Errorf("compose service %q sets tty, which would mix stderr into the MCP stream", name)
	}

	out := Service{
		Name:       name,
		Image:      svc.Image,
		Entrypoint: slices.Clone(svc.Entrypoint),
		Cmd:        slices.Clone(svc.Command),
		WorkingDir: svc.WorkingDir,
		User:       svc.User,
		Tty:        svc.Tty,
		Env:        map[string]string{},
	}
	for k, v := range svc.Environment {
		if v != nil {
			out.Env[k] = *v
		}
	}
	if len(svc.Labels) > 0 {
		out.Labels = map[string]string(svc.Labels)
	}

	pull, err := composePull(name, svc.PullPolicy)
	if err != nil {
		return Service{}, err
	}
	out.Pull = pull

	if out.NetworkMode, err = composeNetworkMode(name, svc.NetworkMode); err != nil {
		return Service{}, err
	}
	for net, cfg := range svc.Networks {
		if cfg == nil {
			continue
		}
		out.Aliases = append(out.Aliases, cfg.Aliases...)
		if hasNetworkSettings(cfg) {
			slog.Warn("Ignoring compose network settings", "service", name, "network", net)
		}
	}
	sort.Strings(out.Aliases)

	for _, v := range svc.Volumes {
		m, err := composeMount(project, name, v)
		if err != nil {
			return Service{}, err
		}
		out.Mounts = append(out.Mounts, m)
	}

	if len(svc.DependsOn) > 0 {
		out.DependsOn = make(map[string]Condition, len(svc.DependsOn))
		for dep, d := range svc.DependsOn {
			cond, err := composeCondition(name, dep, d.Condition)
			if err != nil {
				return Service{}, err
			}
			out.DependsOn[dep] = cond
		}
	}
	if hc := svc.HealthCheck; hc != nil {
		out.Healthcheck = composeHealthcheck(hc)
	}
	return out, nil
}

// NOTE: Works on the marshalled service so a field the library adds later is rejected until it is handled here.
func checkComposeFields(name string, svc types.ServiceConfig) error {
	raw, err := json.Marshal(svc)
	if err != nil {
		return fmt.Errorf("compose service %q: %w", name, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("compose service %q: %w", name, err)
	}

	keys := make([]string, 0, len(fields))
	for k, v := range fields {
		if !emptyJSON(v) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	var problems []string
	for _, k := range keys {
		switch {
		case composeSupported[k]:
		case composeIgnored[k]:
			if k == "build" && svc.Image == "" {
				problems = append(problems, `"build" without an image, since Angela does not build images`)
				continue
			}
			slog.Warn("Ignoring compose field", "service", name, "field", k)
		case composeRejected[k] != "":
			problems = append(problems, fmt.Sprintf("%q is not allowed because %s", k, composeRejected[k]))
		default:
			problems = append(problems, fmt.Sprintf("%q is not supported", k))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("compose service %q: %s", name, strings.Join(problems, "; "))
	}
	return nil
}

func emptyJSON(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "null", `""`, "[]", "{}", "false", "0":
		return true
	}
	return false
}

func hasNetworkSettings(cfg *types.ServiceNetworkConfig) bool {
	c := *cfg
	c.Aliases = nil
	raw, err := json.Marshal(c)
	return err == nil && !emptyJSON(raw)
}

func composePull(service, policy string) (PullPolicy, error) {
	switch policy {
	case "", "missing", "if_not_present":
		return PullMissing, nil
	case "always":
		return PullAlways, nil
	case "never":
		return PullNever, nil
	}
	return "", fmt.Errorf("compose service %q: pull_policy %q is not supported", service, policy)
}

func composeNetworkMode(service, mode string) (string, error) {
	switch mode {
	case "", "default":
		return "", nil
	case "none", "host", "bridge":
		return mode, nil
	}
	return "", fmt.Errorf("compose service %q: network_mode %q is not supported", service, mode)
}

func composeCondition(service, dep, condition string) (Condition, error) {
	switch condition {
	case "", types.ServiceConditionStarted:
		return ConditionStarted, nil
	case types.ServiceConditionHealthy:
		return ConditionHealthy, nil
	case types.ServiceConditionCompletedSuccessfully:
		return ConditionCompleted, nil
	}
	return "", fmt.Errorf("compose service %q: depends_on condition %q for %q is not supported", service, condition, dep)
}

func composeHealthcheck(hc *types.HealthCheckConfig) *Healthcheck {
	if hc.Disable {
		return &Healthcheck{Test: []string{"NONE"}}
	}
	out := &Healthcheck{Test: slices.Clone(hc.Test)}
	dur := func(d *types.Duration) time.Duration {
		if d == nil {
			return 0
		}
		return time.Duration(*d)
	}
	out.Interval, out.Timeout = dur(hc.Interval), dur(hc.Timeout)
	out.StartPeriod, out.StartInterval = dur(hc.StartPeriod), dur(hc.StartInterval)
	if hc.Retries != nil {
		out.Retries = int(*hc.Retries)
	}
	return out
}

func composeMount(project *types.Project, service string, v types.ServiceVolumeConfig) (Mount, error) {
	fail := func(format string, args ...any) (Mount, error) {
		return Mount{}, fmt.Errorf("compose service %q: volume %q: %s", service, v.Target, fmt.Sprintf(format, args...))
	}
	m := Mount{Source: v.Source, Target: v.Target, ReadOnly: v.ReadOnly}
	switch v.Type {
	case types.VolumeTypeBind:
		m.Type = MountBind
		if b := v.Bind; b != nil {
			if b.Propagation != "" || b.Recursive != "" {
				return fail("bind propagation options are not supported")
			}
			m.Relabel = b.SELinux
		}
	case types.VolumeTypeVolume:
		m.Type = MountVolume
		if v.Source == "" {
			return fail("anonymous volumes are not supported, name the volume")
		}
		if vol := v.Volume; vol != nil && (vol.NoCopy || vol.Subpath != "") {
			return fail("nocopy and subpath are not supported")
		}
		if top, ok := project.Volumes[v.Source]; ok {
			if (top.Driver != "" && top.Driver != "local") || len(top.DriverOpts) > 0 {
				return fail("volume %q uses a driver or driver options, which are not supported", v.Source)
			}
			if top.Name != "" {
				m.Source = top.Name
			}
		}
	case types.VolumeTypeTmpfs:
		m.Type = MountTmpfs
		m.Source = ""
		if t := v.Tmpfs; t != nil && (t.Size != 0 || t.Mode != 0) {
			return fail("tmpfs size and mode are not supported")
		}
	default:
		return fail("type %q is not supported", v.Type)
	}
	return m, nil
}
