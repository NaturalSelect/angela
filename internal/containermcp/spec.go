package containermcp

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/NaturalSelect/angela/internal/sandbox"
)

type PullPolicy string

const (
	PullMissing PullPolicy = "missing"
	PullAlways  PullPolicy = "always"
	PullNever   PullPolicy = "never"
)

type MountType string

const (
	MountBind   MountType = "bind"
	MountVolume MountType = "volume"
	MountTmpfs  MountType = "tmpfs"
)

type Mount struct {
	Type     MountType
	Source   string
	Target   string
	ReadOnly bool
	Relabel  string // "", "z" or "Z".
	// NOTE: Tmpfs only, such as size=64m,mode=1777.
	Options string
}

type Service struct {
	Name       string
	Image      string
	Entrypoint []string
	Cmd        []string
	// NOTE: Explicit variables only; the host environment is never inherited.
	Env        map[string]string
	Mounts     []Mount
	WorkingDir string
	User       string
	Pull       PullPolicy

	// NOTE: The fields below come from compose files only.
	Labels  map[string]string
	Aliases []string
	// NetworkMode is empty to join the group network, else none, host or bridge.
	NetworkMode string
	Tty         bool
	DependsOn   map[string]Condition
	Healthcheck *Healthcheck
	// NOTE: ShmSize is in bytes; 0 keeps the engine's default. Init runs a minimal init as PID 1.
	ShmSize int64
	Init    bool
	// NOTE: Entries are host:ip, where ip may be the engine's host-gateway keyword.
	ExtraHosts []string
	// NOTE: These only narrow what the container may do; nothing here can add a privilege.
	CapDrop         []string
	NoNewPrivileges bool
	ReadOnly        bool
	Limits          Limits
	// NOTE: These shape the container's own view of its identity and name resolution.
	Hostname   string
	Domainname string
	DNS        []string
	DNSSearch  []string
	DNSOptions []string
	StopSignal string
}

type Condition string

const (
	ConditionStarted   Condition = "started"
	ConditionHealthy   Condition = "healthy"
	ConditionCompleted Condition = "completed_successfully"
)

// NOTE: Test uses the engine's format: CMD, CMD-SHELL or NONE first. Zero durations inherit the image's value.
type Healthcheck struct {
	Test          []string
	Interval      time.Duration
	Timeout       time.Duration
	StartPeriod   time.Duration
	StartInterval time.Duration
	Retries       int
}

// NOTE: Main is attached over stdio; Deps run in the background in start order.
type Group struct {
	Server string
	Main   Service
	Deps   []Service
	// NOTE: Only used without Deps; a group with dependencies gets its own network.
	Network string
}

var volumeName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

func (g Group) Validate() error {
	switch g.Network {
	case "", "bridge", "none", "host":
	default:
		return fmt.Errorf("network must be bridge, none or host, got %q", g.Network)
	}
	if err := g.Main.validate(); err != nil {
		return err
	}
	started := map[string]bool{}
	for _, dep := range g.Deps {
		if err := dep.validate(); err != nil {
			return err
		}
		if dep.Name == g.Main.Name || started[dep.Name] {
			return fmt.Errorf("service %q appears twice", dep.Name)
		}
		if err := dep.validateOrder(started); err != nil {
			return err
		}
		started[dep.Name] = true
	}
	return g.Main.validateOrder(started)
}

// NOTE: Dependencies must start earlier, so a bad order or a cycle is an error rather than a hang.
func (s Service) validateOrder(started map[string]bool) error {
	for name, cond := range s.DependsOn {
		if !started[name] {
			return fmt.Errorf("service %q depends on %q, which does not start before it", s.Name, name)
		}
		switch cond {
		case ConditionStarted, ConditionHealthy, ConditionCompleted:
		default:
			return fmt.Errorf("service %q has unknown depends_on condition %q", s.Name, cond)
		}
	}
	return nil
}

func (s Service) validate() error {
	if strings.TrimSpace(s.Image) == "" {
		return fmt.Errorf("service %q has no image", s.Name)
	}
	switch s.Pull {
	case "", PullMissing, PullAlways, PullNever:
	default:
		return fmt.Errorf("pull must be missing, always or never, got %q", s.Pull)
	}
	switch s.NetworkMode {
	case "", "none", "host", "bridge":
	default:
		return fmt.Errorf("service %q: network mode must be none, host or bridge, got %q", s.Name, s.NetworkMode)
	}
	if s.Healthcheck != nil && len(s.Healthcheck.Test) > 0 {
		switch s.Healthcheck.Test[0] {
		case "CMD", "CMD-SHELL", "NONE":
		default:
			return fmt.Errorf("service %q: healthcheck test must start with CMD, CMD-SHELL or NONE", s.Name)
		}
	}
	if s.ShmSize < 0 {
		return fmt.Errorf("service %q: shm size must not be negative", s.Name)
	}
	for _, h := range s.ExtraHosts {
		host, ip, ok := strings.Cut(h, ":")
		if !ok || host == "" || ip == "" {
			return fmt.Errorf("service %q: extra host %q must be host:ip", s.Name, h)
		}
	}
	if err := s.validateOptions(); err != nil {
		return fmt.Errorf("service %q: %w", s.Name, err)
	}
	for _, m := range s.Mounts {
		if err := m.validate(); err != nil {
			return fmt.Errorf("service %q: %w", s.Name, err)
		}
	}
	return nil
}

func (m Mount) validate() error {
	if !path.IsAbs(m.Target) {
		return fmt.Errorf("mount target %q must be an absolute path", m.Target)
	}
	switch m.Relabel {
	case "", "z", "Z":
	default:
		return fmt.Errorf("mount %q: relabel must be z or Z", m.Target)
	}
	if m.Options != "" && m.Type != MountTmpfs {
		return fmt.Errorf("mount %q: only a tmpfs mount takes options", m.Target)
	}
	switch m.Type {
	case MountBind:
		return validateBindSource(m.Source)
	case MountVolume:
		if !volumeName.MatchString(m.Source) {
			return fmt.Errorf("invalid volume name %q", m.Source)
		}
	case MountTmpfs:
		if m.Source != "" {
			return fmt.Errorf("tmpfs mount %q cannot have a source", m.Target)
		}
		if err := validateTmpfsOptions(m.Options); err != nil {
			return fmt.Errorf("tmpfs mount %q: %w", m.Target, err)
		}
	default:
		return fmt.Errorf("unknown mount type %q", m.Type)
	}
	return nil
}

// NOTE: A daemon socket lets the container start another one that mounts any host path.
func validateBindSource(source string) error {
	if !filepath.IsAbs(source) {
		return fmt.Errorf("bind mount source %q must be an absolute path", source)
	}
	fi, err := os.Stat(source)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("bind mount source %q does not exist", source)
		}
		return fmt.Errorf("bind mount source %q: %w", source, err)
	}
	if fi.Mode()&os.ModeSocket != 0 {
		return fmt.Errorf("bind mount source %q is a socket", source)
	}
	if socket, ok := exposedDaemonSocket(source); ok {
		return fmt.Errorf("bind mount source %q would expose the container engine socket %s", source, socket)
	}
	return nil
}

func exposedDaemonSocket(source string) (string, bool) {
	dir := canonicalPath(source)
	candidates := sandbox.ContainerSocketCandidates()
	if runtimeDir := os.Getenv("XDG_RUNTIME_DIR"); runtimeDir != "" {
		candidates = append(candidates, filepath.Clean(runtimeDir))
	}
	for _, c := range candidates {
		if within(canonicalPath(c), dir) {
			return c, true
		}
	}
	return "", false
}

// NOTE: Resolves symlinks in the longest existing prefix so /var/run and /run compare equal.
func canonicalPath(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for cur := p; ; cur = filepath.Dir(cur) {
		if resolved, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
	}
}

func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
