package containermcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"
)

type engine interface {
	// NOTE: Empty when the engine does not say.
	CgroupVersion(ctx context.Context) (string, error)
	ImageExists(ctx context.Context, ref string) (bool, error)
	ImagePull(ctx context.Context, ref string) error
	Create(ctx context.Context, spec containerSpec) (string, error)
	Attach(ctx context.Context, id string) (attachment, error)
	Start(ctx context.Context, id string) error
	// NOTE: Call before Start; removed waits for auto-removal instead of the next exit.
	Wait(ctx context.Context, id string, removed bool) <-chan exitResult
	Inspect(ctx context.Context, id string) (containerState, error)
	Stop(ctx context.Context, id string) error
	Kill(ctx context.Context, id string) error
	Remove(ctx context.Context, id string) error
	NetworkCreate(ctx context.Context, name string, labels map[string]string) (string, error)
	NetworkRemove(ctx context.Context, id string) error
	// NOTE: label is "key=value"; containers in every state are listed.
	ListContainers(ctx context.Context, label string) ([]listedContainer, error)
	ListNetworks(ctx context.Context, label string) ([]listedNetwork, error)
	Close() error
}

type containerSpec struct {
	Name    string
	Service Service
	Labels  map[string]string
	// NetworkMode is none, host or bridge; Network names a user-defined network to join. At most one is set.
	NetworkMode string
	Network     string
	Interactive bool
	AutoRemove  bool
}

type containerState struct {
	Status   string
	ExitCode int
	// NOTE: Empty when the container has no healthcheck.
	Health string
}

type listedContainer struct {
	ID      string
	Labels  map[string]string
	State   string
	Created time.Time
}

type listedNetwork struct {
	ID     string
	Labels map[string]string
}

// NOTE: Reads the multiplexed stdout and stderr stream and writes stdin.
type attachment interface {
	io.Reader
	io.Writer
	CloseWrite() error
	Close() error
}

type exitResult struct {
	Code int64
	Err  error
}

type mobyEngine struct {
	cli *client.Client
}

func newMobyEngine(ep Endpoint) (*mobyEngine, error) {
	cli, err := client.New(client.WithHost(ep.Host))
	if err != nil {
		return nil, fmt.Errorf("connect to container engine %s: %w", ep.Host, err)
	}
	return &mobyEngine{cli: cli}, nil
}

func (e *mobyEngine) CgroupVersion(ctx context.Context) (string, error) {
	res, err := e.cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return "", err
	}
	return res.Info.CgroupVersion, nil
}

func (e *mobyEngine) ImageExists(ctx context.Context, ref string) (bool, error) {
	_, err := e.cli.ImageInspect(ctx, ref)
	if err == nil {
		return true, nil
	}
	if cerrdefs.IsNotFound(err) {
		return false, nil
	}
	return false, err
}

func (e *mobyEngine) ImagePull(ctx context.Context, ref string) error {
	resp, err := e.cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return err
	}
	defer resp.Close()
	return resp.Wait(ctx)
}

func (e *mobyEngine) Create(ctx context.Context, spec containerSpec) (string, error) {
	svc := spec.Service
	labels := make(map[string]string, len(svc.Labels)+len(spec.Labels))
	maps.Copy(labels, svc.Labels)
	maps.Copy(labels, spec.Labels)
	cfg := &container.Config{
		Image:        svc.Image,
		Cmd:          svc.Cmd,
		Entrypoint:   svc.Entrypoint,
		Env:          envList(svc.Env),
		User:         svc.User,
		WorkingDir:   svc.WorkingDir,
		Labels:       labels,
		Tty:          svc.Tty,
		AttachStdin:  spec.Interactive,
		AttachStdout: spec.Interactive,
		AttachStderr: spec.Interactive,
		OpenStdin:    spec.Interactive,
		StdinOnce:    spec.Interactive,
	}
	if hc := svc.Healthcheck; hc != nil {
		cfg.Healthcheck = &container.HealthConfig{
			Test:          hc.Test,
			Interval:      hc.Interval,
			Timeout:       hc.Timeout,
			StartPeriod:   hc.StartPeriod,
			StartInterval: hc.StartInterval,
			Retries:       hc.Retries,
		}
	}
	host := &container.HostConfig{
		AutoRemove:  spec.AutoRemove,
		NetworkMode: container.NetworkMode(spec.NetworkMode),
		ShmSize:     svc.ShmSize,
		ExtraHosts:  svc.ExtraHosts,
	}
	if svc.Init {
		host.Init = new(true)
	}
	if err := applyOptions(cfg, host, svc); err != nil {
		return "", err
	}
	var networking *network.NetworkingConfig
	if spec.Network != "" {
		host.NetworkMode = container.NetworkMode(spec.Network)
		networking = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			spec.Network: {Aliases: append([]string{svc.Name}, svc.Aliases...)},
		}}
	}
	for _, m := range svc.Mounts {
		if m.Type == MountTmpfs {
			if host.Tmpfs == nil {
				host.Tmpfs = map[string]string{}
			}
			host.Tmpfs[m.Target] = m.Options
			continue
		}
		host.Binds = append(host.Binds, bindString(m))
	}

	res, err := e.cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Config:           cfg,
		HostConfig:       host,
		NetworkingConfig: networking,
		Name:             spec.Name,
	})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

func applyOptions(cfg *container.Config, host *container.HostConfig, svc Service) error {
	cfg.Hostname, cfg.Domainname, cfg.StopSignal = svc.Hostname, svc.Domainname, svc.StopSignal
	host.CapDrop = svc.CapDrop
	host.ReadonlyRootfs = svc.ReadOnly
	if svc.NoNewPrivileges {
		host.SecurityOpt = []string{"no-new-privileges"}
	}
	for _, s := range svc.DNS {
		addr, err := netip.ParseAddr(s)
		if err != nil {
			return fmt.Errorf("dns entry %q: %w", s, err)
		}
		host.DNS = append(host.DNS, addr)
	}
	host.DNSSearch, host.DNSOptions = svc.DNSSearch, svc.DNSOptions

	l := svc.Limits
	host.Memory, host.MemoryReservation, host.MemorySwap = l.Memory, l.MemoryReservation, l.MemorySwap
	host.NanoCPUs, host.CPUShares, host.CPUPeriod, host.CPUQuota = l.NanoCPUs, l.CPUShares, l.CPUPeriod, l.CPUQuota
	host.CpusetCpus = l.CPUSet
	host.MemorySwappiness = l.MemorySwappiness
	if l.PidsLimit > 0 {
		host.PidsLimit = &l.PidsLimit
	}
	return nil
}

func (e *mobyEngine) Attach(ctx context.Context, id string) (attachment, error) {
	res, err := e.cli.ContainerAttach(ctx, id, client.ContainerAttachOptions{
		Stream: true,
		Stdin:  true,
		Stdout: true,
		Stderr: true,
	})
	if err != nil {
		return nil, err
	}
	return &hijackedAttachment{resp: res.HijackedResponse}, nil
}

func (e *mobyEngine) Start(ctx context.Context, id string) error {
	_, err := e.cli.ContainerStart(ctx, id, client.ContainerStartOptions{})
	return err
}

func (e *mobyEngine) Wait(ctx context.Context, id string, removed bool) <-chan exitResult {
	cond := container.WaitConditionNextExit
	if removed {
		cond = container.WaitConditionRemoved
	}
	res := e.cli.ContainerWait(ctx, id, client.ContainerWaitOptions{Condition: cond})
	out := make(chan exitResult, 1)
	go func() {
		select {
		case r := <-res.Result:
			if r.Error != nil {
				out <- exitResult{Code: r.StatusCode, Err: errors.New(r.Error.Message)}
				return
			}
			out <- exitResult{Code: r.StatusCode}
		case err := <-res.Error:
			out <- exitResult{Err: err}
		case <-ctx.Done():
			out <- exitResult{Err: ctx.Err()}
		}
	}()
	return out
}

func (e *mobyEngine) Inspect(ctx context.Context, id string) (containerState, error) {
	res, err := e.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return containerState{}, err
	}
	st := res.Container.State
	if st == nil {
		return containerState{}, errors.New("engine returned no container state")
	}
	out := containerState{Status: string(st.Status), ExitCode: st.ExitCode}
	if st.Health != nil && st.Health.Status != container.NoHealthcheck {
		out.Health = string(st.Health.Status)
	}
	return out, nil
}

func (e *mobyEngine) Stop(ctx context.Context, id string) error {
	timeout := int(stopTimeout / time.Second)
	_, err := e.cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &timeout})
	return err
}

func (e *mobyEngine) Kill(ctx context.Context, id string) error {
	_, err := e.cli.ContainerKill(ctx, id, client.ContainerKillOptions{})
	return err
}

// NOTE: Podman waits out the stop timeout before a forced removal, so a best-effort kill keeps cleanup inside its deadline.
func (e *mobyEngine) Remove(ctx context.Context, id string) error {
	_, _ = e.cli.ContainerKill(ctx, id, client.ContainerKillOptions{})
	_, err := e.cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
	return err
}

func (e *mobyEngine) NetworkCreate(ctx context.Context, name string, labels map[string]string) (string, error) {
	res, err := e.cli.NetworkCreate(ctx, name, client.NetworkCreateOptions{Labels: labels})
	if err != nil {
		return "", err
	}
	return res.ID, nil
}

func (e *mobyEngine) NetworkRemove(ctx context.Context, id string) error {
	_, err := e.cli.NetworkRemove(ctx, id, client.NetworkRemoveOptions{})
	return err
}

func (e *mobyEngine) ListContainers(ctx context.Context, label string) ([]listedContainer, error) {
	res, err := e.cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: make(client.Filters).Add("label", label)})
	if err != nil {
		return nil, err
	}
	out := make([]listedContainer, 0, len(res.Items))
	for _, c := range res.Items {
		out = append(out, listedContainer{ID: c.ID, Labels: c.Labels, State: string(c.State), Created: time.Unix(c.Created, 0)})
	}
	return out, nil
}

func (e *mobyEngine) ListNetworks(ctx context.Context, label string) ([]listedNetwork, error) {
	res, err := e.cli.NetworkList(ctx, client.NetworkListOptions{Filters: make(client.Filters).Add("label", label)})
	if err != nil {
		return nil, err
	}
	out := make([]listedNetwork, 0, len(res.Items))
	for _, n := range res.Items {
		out = append(out, listedNetwork{ID: n.ID, Labels: n.Labels})
	}
	return out, nil
}

func (e *mobyEngine) Close() error {
	return e.cli.Close()
}

type hijackedAttachment struct {
	resp client.HijackedResponse
}

func (a *hijackedAttachment) Read(p []byte) (int, error)  { return a.resp.Reader.Read(p) }
func (a *hijackedAttachment) Write(p []byte) (int, error) { return a.resp.Conn.Write(p) }

// NOTE: The MCP SDK closes the writer after the reader has already ended the session, so a closed connection is fine.
func (a *hijackedAttachment) CloseWrite() error {
	if err := a.resp.CloseWrite(); err != nil && !errors.Is(err, net.ErrClosed) {
		return err
	}
	return nil
}

func (a *hijackedAttachment) Close() error {
	a.resp.Close()
	return nil
}

func envList(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func bindString(m Mount) string {
	s := m.Source + ":" + m.Target
	var opts []string
	if m.ReadOnly {
		opts = append(opts, "ro")
	}
	if m.Relabel != "" {
		opts = append(opts, m.Relabel)
	}
	if len(opts) > 0 {
		s += ":" + strings.Join(opts, ",")
	}
	return s
}
