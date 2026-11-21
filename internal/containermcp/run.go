package containermcp

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	LabelServer = "dev.angela.mcp.server"
	LabelRole   = "dev.angela.mcp.role"
	LabelGroup  = "dev.angela.mcp.group"

	roleMain = "main"
	roleDep  = "dep"

	cleanupTimeout = 10 * time.Second
	exitWait       = 2 * time.Second
	stderrTailSize = 4096
	stopTimeout    = 5 * time.Second
	// NOTE: A created main container older than this is a crashed startup, not one in progress.
	orphanGrace = 10 * time.Minute
)

var (
	stdinGrace = 5 * time.Second
	healthPoll = 500 * time.Millisecond
)

type Session struct {
	server     string
	eng        engine
	id         string
	att        attachment
	stdout     *io.PipeReader
	stderr     *stderrSink
	exited     chan struct{}
	exitMu     sync.Mutex
	exitRes    exitResult
	closeOnce  sync.Once
	deps       []startedDep
	networkID  string
	cancelWait context.CancelFunc
}

type startedDep struct {
	name string
	id   string
	exit <-chan exitResult
}

func Open(ctx context.Context, ep Endpoint, g Group) (*Session, error) {
	eng, err := newMobyEngine(ep)
	if err != nil {
		return nil, err
	}
	return start(ctx, eng, g)
}

// NOTE: Takes ownership of eng and closes it on failure.
func start(ctx context.Context, eng engine, g Group) (_ *Session, err error) {
	defer func() {
		if err != nil {
			_ = eng.Close()
		}
	}()
	if err := g.Validate(); err != nil {
		return nil, err
	}
	if len(g.Deps) > 0 {
		sweepOrphans(ctx, eng, g.Server)
	}
	for _, svc := range append(slices.Clone(g.Deps), g.Main) {
		if err := ensureImage(ctx, eng, svc.Image, svc.Pull); err != nil {
			return nil, err
		}
	}

	// NOTE: The exit watchers must outlive the startup context, which the caller cancels after connecting.
	waitCtx, cancelWait := context.WithCancel(context.WithoutCancel(ctx))
	l := &launch{eng: eng, g: g, groupID: newGroupID(), waitCtx: waitCtx, started: map[string]startedDep{}}
	defer func() {
		if err != nil {
			cancelWait()
			l.discard()
		}
	}()

	if l.needsNetwork() {
		l.networkName = "angela-mcp-" + safeName(g.Server) + "-" + l.groupID
		l.networkID, err = eng.NetworkCreate(ctx, l.networkName, l.labels(""))
		if err != nil {
			return nil, fmt.Errorf("create network: %w", err)
		}
	}

	// NOTE: The main container is created first so a concurrent sweep sees the group as alive.
	l.mainID, err = eng.Create(ctx, l.spec(g.Main, roleMain))
	if err != nil {
		return nil, fmt.Errorf("create container: %w", err)
	}
	mainExit := eng.Wait(waitCtx, l.mainID, true)

	for _, dep := range g.Deps {
		if err := l.startDep(ctx, dep); err != nil {
			return nil, err
		}
	}
	if err := l.waitFor(ctx, g.Main); err != nil {
		return nil, err
	}

	att, err := eng.Attach(ctx, l.mainID)
	if err != nil {
		return nil, fmt.Errorf("attach to container: %w", err)
	}
	if err := eng.Start(ctx, l.mainID); err != nil {
		_ = att.Close()
		return nil, fmt.Errorf("start container: %w", err)
	}

	stdoutR, stdoutW := io.Pipe()
	s := &Session{
		server:     g.Server,
		eng:        eng,
		id:         l.mainID,
		att:        att,
		stdout:     stdoutR,
		stderr:     &stderrSink{server: g.Server},
		exited:     make(chan struct{}),
		deps:       l.depList,
		networkID:  l.networkID,
		cancelWait: cancelWait,
	}
	go func() {
		_, err := stdcopy.StdCopy(stdoutW, s.stderr, att)
		stdoutW.CloseWithError(err)
	}()
	go func() {
		res := <-mainExit
		s.exitMu.Lock()
		s.exitRes = res
		s.exitMu.Unlock()
		close(s.exited)
	}()
	return s, nil
}

// NOTE: Tracks what one startup has created so a failure can remove all of it.
type launch struct {
	eng         engine
	g           Group
	groupID     string
	waitCtx     context.Context
	networkName string
	networkID   string
	mainID      string
	depList     []startedDep
	started     map[string]startedDep
}

func (l *launch) needsNetwork() bool {
	if len(l.g.Deps) == 0 {
		return false
	}
	for _, svc := range append(slices.Clone(l.g.Deps), l.g.Main) {
		if svc.NetworkMode == "" {
			return true
		}
	}
	return false
}

func (l *launch) labels(role string) map[string]string {
	labels := map[string]string{LabelServer: l.g.Server, LabelGroup: l.groupID}
	if role != "" {
		labels[LabelRole] = role
	}
	return labels
}

func (l *launch) spec(svc Service, role string) containerSpec {
	spec := containerSpec{
		Service:     svc,
		Labels:      l.labels(role),
		Interactive: role == roleMain,
		AutoRemove:  role == roleMain,
	}
	switch {
	case svc.NetworkMode != "":
		spec.NetworkMode = svc.NetworkMode
	case l.networkName != "":
		spec.Network = l.networkName
	default:
		spec.NetworkMode = l.g.Network
	}
	return spec
}

func (l *launch) startDep(ctx context.Context, dep Service) error {
	if err := l.waitFor(ctx, dep); err != nil {
		return err
	}
	id, err := l.eng.Create(ctx, l.spec(dep, roleDep))
	if err != nil {
		return fmt.Errorf("create service %q: %w", dep.Name, err)
	}
	d := startedDep{name: dep.Name, id: id}
	d.exit = l.eng.Wait(l.waitCtx, id, false)
	l.depList = append(l.depList, d)
	l.started[dep.Name] = d
	if err := l.eng.Start(ctx, id); err != nil {
		return fmt.Errorf("start service %q: %w", dep.Name, err)
	}
	return nil
}

// NOTE: Waits in name order so a failure message is stable when several conditions would fail.
func (l *launch) waitFor(ctx context.Context, svc Service) error {
	names := slices.Sorted(maps.Keys(svc.DependsOn))
	for _, name := range names {
		dep := l.started[name]
		var err error
		switch svc.DependsOn[name] {
		case ConditionHealthy:
			err = l.waitHealthy(ctx, dep)
		case ConditionCompleted:
			err = l.waitCompleted(ctx, dep)
		}
		if err != nil {
			return fmt.Errorf("service %q waits for %q: %w", svc.Name, name, err)
		}
	}
	return nil
}

func (l *launch) waitHealthy(ctx context.Context, dep startedDep) error {
	ticker := time.NewTicker(healthPoll)
	defer ticker.Stop()
	for {
		st, err := l.eng.Inspect(ctx, dep.id)
		if err != nil {
			return fmt.Errorf("inspect: %w", err)
		}
		switch {
		case st.Health == "healthy":
			return nil
		case st.Health == "unhealthy":
			return errors.New("it became unhealthy")
		case st.Status == "exited" || st.Status == "dead":
			return fmt.Errorf("it exited with code %d before becoming healthy", st.ExitCode)
		case st.Health == "":
			return errors.New("it has no healthcheck")
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return fmt.Errorf("it did not become healthy: %w", ctx.Err())
		}
	}
}

// NOTE: The state is polled too because Podman 4 can miss the exit of a container that finishes within its own poll interval.
func (l *launch) waitCompleted(ctx context.Context, dep startedDep) error {
	ticker := time.NewTicker(healthPoll)
	defer ticker.Stop()
	for {
		select {
		case res := <-dep.exit:
			if res.Err != nil {
				return fmt.Errorf("it stopped: %w", res.Err)
			}
			return completedError(res.Code)
		case <-ticker.C:
			st, err := l.eng.Inspect(ctx, dep.id)
			if err != nil {
				return fmt.Errorf("inspect: %w", err)
			}
			if st.Status == "exited" || st.Status == "dead" {
				return completedError(int64(st.ExitCode))
			}
		case <-ctx.Done():
			return fmt.Errorf("it did not finish: %w", ctx.Err())
		}
	}
}

func completedError(code int64) error {
	if code != 0 {
		return fmt.Errorf("it exited with code %d", code)
	}
	return nil
}

// NOTE: Forced removal is enough on a failed startup; nothing needs a graceful stop yet.
func (l *launch) discard() {
	if l.mainID != "" {
		removeQuietly(l.eng, l.mainID)
	}
	for i := len(l.depList) - 1; i >= 0; i-- {
		removeQuietly(l.eng, l.depList[i].id)
	}
	removeNetworkQuietly(l.eng, l.networkID)
}

func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			return r
		}
		return '-'
	}, s)
}

// NOTE: A crashed Angela leaves dependency containers behind; a group whose main container is gone is such a leftover.
func sweepOrphans(ctx context.Context, eng engine, server string) {
	ctx, cancel := context.WithTimeout(ctx, cleanupTimeout)
	defer cancel()

	label := LabelServer + "=" + server
	containers, err := eng.ListContainers(ctx, label)
	if err != nil {
		slog.Debug("Failed to list MCP containers", "server", server, "error", err)
		return
	}
	alive := map[string]bool{}
	for _, c := range containers {
		if c.Labels[LabelRole] == roleMain && mainAlive(c) {
			alive[c.Labels[LabelGroup]] = true
		}
	}
	for _, c := range containers {
		gid := c.Labels[LabelGroup]
		if gid == "" || alive[gid] {
			continue
		}
		slog.Info("Removing leftover MCP container", "server", server, "id", c.ID)
		if err := eng.Remove(ctx, c.ID); err != nil && !cerrdefs.IsNotFound(err) {
			slog.Debug("Failed to remove leftover MCP container", "id", c.ID, "error", err)
		}
	}
	networks, err := eng.ListNetworks(ctx, label)
	if err != nil {
		slog.Debug("Failed to list MCP networks", "server", server, "error", err)
		return
	}
	for _, n := range networks {
		if gid := n.Labels[LabelGroup]; gid != "" && !alive[gid] {
			if err := eng.NetworkRemove(ctx, n.ID); err != nil && !cerrdefs.IsNotFound(err) {
				slog.Debug("Failed to remove leftover MCP network", "id", n.ID, "error", err)
			}
		}
	}
}

func mainAlive(c listedContainer) bool {
	switch c.State {
	case "running":
		return true
	case "created":
		return time.Since(c.Created) < orphanGrace
	}
	return false
}

func ensureImage(ctx context.Context, eng engine, image string, pull PullPolicy) error {
	if pull == PullAlways {
		return pullImage(ctx, eng, image)
	}
	exists, err := eng.ImageExists(ctx, image)
	if err != nil {
		return fmt.Errorf("inspect image %s: %w", image, err)
	}
	if exists {
		return nil
	}
	if pull == PullNever {
		return fmt.Errorf("image %s is not present and pull is never", image)
	}
	return pullImage(ctx, eng, image)
}

func pullImage(ctx context.Context, eng engine, image string) error {
	if err := eng.ImagePull(ctx, image); err != nil {
		return fmt.Errorf("pull image %s: %w", image, err)
	}
	return nil
}

func newGroupID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func removeQuietly(eng engine, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if err := eng.Remove(ctx, id); err != nil && !cerrdefs.IsNotFound(err) {
		slog.Debug("Failed to remove MCP container", "id", id, "error", err)
	}
}

func removeNetworkQuietly(eng engine, id string) {
	if id == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	if err := eng.NetworkRemove(ctx, id); err != nil && !cerrdefs.IsNotFound(err) {
		slog.Debug("Failed to remove MCP network", "id", id, "error", err)
	}
}

// NOTE: Dependencies stop in reverse start order, each given time to exit on its own.
func stopQuietly(eng engine, dep startedDep) {
	ctx, cancel := context.WithTimeout(context.Background(), stopTimeout+cleanupTimeout)
	defer cancel()
	if err := eng.Stop(ctx, dep.id); err != nil && !cerrdefs.IsNotFound(err) {
		slog.Debug("Failed to stop MCP service", "service", dep.name, "error", err)
	}
	removeQuietly(eng, dep.id)
}

func (s *Session) Transport() mcp.Transport {
	return &mcp.IOTransport{
		Reader: &sessionReader{PipeReader: s.stdout, s: s},
		Writer: stdinWriter{att: s.att},
	}
}

// NOTE: Returns nil while the container still runs.
func (s *Session) ExitErr() error {
	select {
	case <-s.exited:
	case <-time.After(exitWait):
		return nil
	}
	s.exitMu.Lock()
	res := s.exitRes
	s.exitMu.Unlock()

	msg := fmt.Sprintf("container exited with code %d", res.Code)
	if res.Err != nil {
		msg = fmt.Sprintf("container stopped: %v", res.Err)
	}
	if tail := s.stderr.Tail(); tail != "" {
		msg += ": " + tail
	}
	return errors.New(msg)
}

// NOTE: Closing stdin lets a well-behaved server exit; the kill and remove only back that up.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		_ = s.att.CloseWrite()
		select {
		case <-s.exited:
		case <-time.After(stdinGrace):
		}

		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()
		select {
		case <-s.exited:
		default:
			if err := s.eng.Kill(ctx, s.id); err != nil && !cerrdefs.IsNotFound(err) {
				slog.Debug("Failed to kill MCP container", "server", s.server, "error", err)
			}
		}
		if err := s.eng.Remove(ctx, s.id); err != nil && !cerrdefs.IsNotFound(err) {
			slog.Debug("Failed to remove MCP container", "server", s.server, "error", err)
		}
		for i := len(s.deps) - 1; i >= 0; i-- {
			stopQuietly(s.eng, s.deps[i])
		}
		removeNetworkQuietly(s.eng, s.networkID)
		s.cancelWait()
		_ = s.att.Close()
		_ = s.eng.Close()
	})
	return nil
}

type sessionReader struct {
	*io.PipeReader
	s *Session
}

func (r *sessionReader) Close() error {
	err := r.PipeReader.Close()
	_ = r.s.Close()
	return err
}

type stdinWriter struct {
	att attachment
}

func (w stdinWriter) Write(p []byte) (int, error) { return w.att.Write(p) }
func (w stdinWriter) Close() error                { return w.att.CloseWrite() }

// NOTE: Logs stderr by line and keeps its end for error messages.
type stderrSink struct {
	server string
	mu     sync.Mutex
	line   []byte
	tail   []byte
}

func (s *stderrSink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tail = append(s.tail, p...)
	if len(s.tail) > stderrTailSize {
		s.tail = s.tail[len(s.tail)-stderrTailSize:]
	}

	s.line = append(s.line, p...)
	for {
		i := bytes.IndexByte(s.line, '\n')
		if i < 0 {
			break
		}
		slog.Debug("MCP container stderr", "server", s.server, "line", strings.TrimRight(string(s.line[:i]), "\r"))
		s.line = s.line[i+1:]
	}
	if len(s.line) > stderrTailSize {
		s.line = s.line[len(s.line)-stderrTailSize:]
	}
	return len(p), nil
}

func (s *stderrSink) Tail() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.TrimSpace(string(s.tail))
}
