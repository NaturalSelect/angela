package containermcp

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/home"
)

// NOTE: fieldRes expands the fields naming what to run; argsEnvRes expands args and env like stdio servers do.
func FromConfig(ctx context.Context, name string, m config.MCPConfig, workDir string, fieldRes, argsEnvRes config.VariableResolver) (Group, error) {
	if err := rejectForeignFields(m); err != nil {
		return Group{}, err
	}

	resolve := func(field, value string) (string, error) {
		out, err := fieldRes.ResolveValue(value)
		if err != nil {
			return "", fmt.Errorf("%s: %w", field, err)
		}
		return strings.TrimSpace(out), nil
	}

	if m.Compose != "" || m.Service != "" {
		return fromComposeConfig(ctx, name, m, workDir, resolve)
	}

	image, err := resolve("image", m.Image)
	if err != nil {
		return Group{}, err
	}
	network, err := resolve("network", m.Network)
	if err != nil {
		return Group{}, err
	}
	containerDir, err := resolve("workdir", m.Workdir)
	if err != nil {
		return Group{}, err
	}

	mounts := make([]Mount, 0, len(m.Volumes))
	for i, spec := range m.Volumes {
		expanded, err := resolve(fmt.Sprintf("volumes[%d]", i), spec)
		if err != nil {
			return Group{}, err
		}
		mount, err := parseVolume(expanded, workDir)
		if err != nil {
			return Group{}, fmt.Errorf("volumes[%d]: %w", i, err)
		}
		mounts = append(mounts, mount)
	}

	args, err := m.ResolvedArgs(argsEnvRes)
	if err != nil {
		return Group{}, err
	}
	envList, err := m.ResolvedEnv(argsEnvRes)
	if err != nil {
		return Group{}, err
	}
	env := make(map[string]string, len(envList))
	for _, kv := range envList {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}

	g := Group{
		Server: name,
		Main: Service{
			Name:       name,
			Image:      image,
			Cmd:        args,
			Env:        env,
			Mounts:     mounts,
			WorkingDir: containerDir,
		},
		Network: network,
	}
	if err := g.Validate(); err != nil {
		return Group{}, err
	}
	return g, nil
}

// NOTE: The compose file owns everything about the containers, so the per-image fields are refused instead of merged.
func fromComposeConfig(ctx context.Context, name string, m config.MCPConfig, workDir string, resolve func(field, value string) (string, error)) (Group, error) {
	var conflict string
	switch {
	case m.Image != "":
		return Group{}, errors.New("container mcp config sets both 'image' and 'compose'; use one")
	case m.Compose == "":
		return Group{}, errors.New("container mcp config sets 'service' without 'compose'")
	case m.Service == "":
		return Group{}, errors.New("container mcp config sets 'compose' without 'service'")
	case len(m.Args) > 0:
		conflict = "args"
	case len(m.Env) > 0:
		conflict = "env"
	case len(m.Volumes) > 0:
		conflict = "volumes"
	case m.Network != "":
		conflict = "network"
	case m.Workdir != "":
		conflict = "workdir"
	}
	if conflict != "" {
		return Group{}, fmt.Errorf("container mcp config with 'compose' cannot set '%s'; set it in the compose file", conflict)
	}

	file, err := resolve("compose", m.Compose)
	if err != nil {
		return Group{}, err
	}
	service, err := resolve("service", m.Service)
	if err != nil {
		return Group{}, err
	}
	g, err := FromCompose(ctx, name, file, service, workDir)
	if err != nil {
		return Group{}, err
	}
	return g, nil
}

func rejectForeignFields(m config.MCPConfig) error {
	switch {
	case m.Command != "":
		return fmt.Errorf("container mcp config cannot set 'command'; use 'image' and 'args'")
	case m.URL != "":
		return fmt.Errorf("container mcp config cannot set 'url'")
	case len(m.Headers) > 0:
		return fmt.Errorf("container mcp config cannot set 'headers'")
	case m.OAuth || m.OAuthClientID != "" || m.OAuthClientSecret != "" || m.OAuthCallbackPort != 0:
		return fmt.Errorf("container mcp config cannot use oauth")
	}
	return nil
}

// NOTE: A source starting with "/", "." or "~" is a host path, anything else a named volume.
func parseVolume(spec, workDir string) (Mount, error) {
	parts := splitVolume(spec, runtime.GOOS == "windows")
	if len(parts) < 2 || len(parts) > 3 {
		return Mount{}, fmt.Errorf("volume %q must be source:target[:options]", spec)
	}
	source, target := parts[0], parts[1]
	if source == "" {
		return Mount{}, fmt.Errorf("volume %q has an empty source", spec)
	}

	mount := Mount{Source: source, Target: target}
	if len(parts) == 3 {
		for _, opt := range strings.Split(parts[2], ",") {
			switch opt {
			case "ro":
				mount.ReadOnly = true
			case "rw":
				mount.ReadOnly = false
			case "z", "Z":
				if mount.Relabel != "" && mount.Relabel != opt {
					return Mount{}, fmt.Errorf("volume %q sets both z and Z", spec)
				}
				mount.Relabel = opt
			default:
				return Mount{}, fmt.Errorf("volume %q has unknown option %q", spec, opt)
			}
		}
	}

	if !isHostPath(source) {
		mount.Type = MountVolume
		return mount, nil
	}
	mount.Type = MountBind
	source = home.Long(source)
	if !filepath.IsAbs(source) {
		if workDir == "" {
			return Mount{}, fmt.Errorf("volume %q is relative but the project directory is unknown", spec)
		}
		source = filepath.Join(workDir, source)
	}
	mount.Source = filepath.Clean(source)
	return mount, nil
}

func isHostPath(source string) bool {
	return filepath.IsAbs(source) || strings.HasPrefix(source, "/") || strings.HasPrefix(source, ".") || strings.HasPrefix(source, "~")
}

// NOTE: On Windows a drive prefix such as C:\data stays in the first part; elsewhere "a:/b" is a volume named a.
func splitVolume(spec string, windows bool) []string {
	drive := ""
	if windows && len(spec) >= 3 && spec[1] == ':' && (spec[2] == '\\' || spec[2] == '/') &&
		(spec[0] >= 'a' && spec[0] <= 'z' || spec[0] >= 'A' && spec[0] <= 'Z') {
		drive = spec[:2]
		spec = spec[2:]
	}
	parts := strings.Split(spec, ":")
	parts[0] = drive + parts[0]
	return parts
}
