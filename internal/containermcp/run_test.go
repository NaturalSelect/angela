package containermcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func frame(stream byte, data string) []byte {
	header := make([]byte, 8)
	header[0] = stream
	binary.BigEndian.PutUint32(header[4:], uint32(len(data)))
	return append(header, data...)
}

type fakeAttachment struct {
	out *io.PipeReader
	in  *io.PipeWriter

	mu          sync.Mutex
	written     bytes.Buffer
	closedWrite bool
	closed      bool
	onCloseW    func()
}

func newFakeAttachment() *fakeAttachment {
	pr, pw := io.Pipe()
	return &fakeAttachment{out: pr, in: pw}
}

func (a *fakeAttachment) Read(p []byte) (int, error) { return a.out.Read(p) }

func (a *fakeAttachment) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.written.Write(p)
}

func (a *fakeAttachment) CloseWrite() error {
	a.mu.Lock()
	a.closedWrite = true
	cb := a.onCloseW
	a.mu.Unlock()
	if cb != nil {
		cb()
	}
	return nil
}

func (a *fakeAttachment) Close() error {
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	_ = a.in.Close()
	return nil
}

func (a *fakeAttachment) feed(t *testing.T, data []byte) {
	t.Helper()
	_, err := a.in.Write(data)
	require.NoError(t, err)
}

func (a *fakeAttachment) writtenString() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.written.String()
}

func (a *fakeAttachment) state() (closedWrite, closed bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closedWrite, a.closed
}

type fakeEngine struct {
	mu      sync.Mutex
	calls   []string
	created []containerSpec
	pulled  []string

	exists    bool
	pullErr   error
	createErr error
	attachErr error
	startErr  error

	att  *fakeAttachment
	exit chan exitResult
}

func newFakeEngine() *fakeEngine {
	f := &fakeEngine{exists: true, att: newFakeAttachment(), exit: make(chan exitResult, 1)}
	f.att.onCloseW = func() {
		select {
		case f.exit <- exitResult{}:
		default:
		}
	}
	return f
}

func (f *fakeEngine) record(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeEngine) callList() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeEngine) ImageExists(context.Context, string) (bool, error) {
	f.record("exists")
	return f.exists, nil
}

func (f *fakeEngine) ImagePull(_ context.Context, ref string) error {
	f.record("pull")
	f.mu.Lock()
	f.pulled = append(f.pulled, ref)
	f.mu.Unlock()
	return f.pullErr
}

func (f *fakeEngine) Create(_ context.Context, spec containerSpec) (string, error) {
	f.record("create")
	if f.createErr != nil {
		return "", f.createErr
	}
	f.mu.Lock()
	f.created = append(f.created, spec)
	f.mu.Unlock()
	return "cid", nil
}

func (f *fakeEngine) Attach(context.Context, string) (attachment, error) {
	f.record("attach")
	if f.attachErr != nil {
		return nil, f.attachErr
	}
	return f.att, nil
}

func (f *fakeEngine) Start(context.Context, string) error {
	f.record("start")
	return f.startErr
}

func (f *fakeEngine) Wait(_ context.Context, _ string, removed bool) <-chan exitResult {
	if removed {
		f.record("wait-removed")
	} else {
		f.record("wait")
	}
	return f.exit
}

func (f *fakeEngine) Kill(context.Context, string) error {
	f.record("kill")
	return nil
}

func (f *fakeEngine) Remove(context.Context, string) error {
	f.record("remove")
	return nil
}

func (f *fakeEngine) Inspect(context.Context, string) (containerState, error) {
	f.record("inspect")
	return containerState{Status: "running"}, nil
}

func (f *fakeEngine) Stop(context.Context, string) error {
	f.record("stop")
	return nil
}

func (f *fakeEngine) NetworkCreate(context.Context, string, map[string]string) (string, error) {
	f.record("network-create")
	return "nid", nil
}

func (f *fakeEngine) NetworkRemove(context.Context, string) error {
	f.record("network-remove")
	return nil
}

func (f *fakeEngine) ListContainers(context.Context, string) ([]listedContainer, error) {
	f.record("list")
	return nil, nil
}

func (f *fakeEngine) ListNetworks(context.Context, string) ([]listedNetwork, error) {
	f.record("list-networks")
	return nil, nil
}

func (f *fakeEngine) Close() error {
	f.record("close")
	return nil
}

func TestStart_CreateAttachStartOrderAndSpec(t *testing.T) {
	t.Parallel()

	eng := newFakeEngine()
	g := validGroup()
	g.Network = "none"
	g.Main.Cmd = []string{"--flag"}

	s, err := start(t.Context(), eng, g)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	require.Equal(t, []string{"exists", "create", "wait-removed", "attach", "start"}, eng.callList())
	require.Len(t, eng.created, 1)
	spec := eng.created[0]
	require.Equal(t, "none", spec.NetworkMode)
	require.True(t, spec.Interactive)
	require.True(t, spec.AutoRemove)
	require.Equal(t, g.Main, spec.Service)
	require.Equal(t, "srv", spec.Labels[LabelServer])
	require.Equal(t, roleMain, spec.Labels[LabelRole])
	require.Len(t, spec.Labels[LabelGroup], 12)
}

func TestStart_PullPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		pull       PullPolicy
		exists     bool
		wantPulled bool
		wantErr    string
	}{
		{"missing and present", PullMissing, true, false, ""},
		{"missing and absent", PullMissing, false, true, ""},
		{"default and absent", "", false, true, ""},
		{"always and present", PullAlways, true, true, ""},
		{"never and present", PullNever, true, false, ""},
		{"never and absent", PullNever, false, false, "pull is never"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			eng := newFakeEngine()
			eng.exists = tc.exists
			g := validGroup()
			g.Main.Pull = tc.pull

			s, err := start(t.Context(), eng, g)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.Contains(t, eng.callList(), "close", "engine is closed when start fails")
				require.NotContains(t, eng.callList(), "create")
				return
			}
			require.NoError(t, err)
			t.Cleanup(func() { _ = s.Close() })
			require.Equal(t, tc.wantPulled, slices.Contains(eng.callList(), "pull"))
			if tc.wantPulled {
				require.Equal(t, []string{"img:1"}, eng.pulled)
			}
		})
	}
}

func TestStart_PullFailure(t *testing.T) {
	t.Parallel()

	eng := newFakeEngine()
	eng.exists = false
	eng.pullErr = errors.New("denied")

	_, err := start(t.Context(), eng, validGroup())
	require.ErrorContains(t, err, "pull image img:1")
	require.ErrorContains(t, err, "denied")
	require.NotContains(t, eng.callList(), "create")
}

func TestStart_FailuresRemoveTheContainer(t *testing.T) {
	t.Parallel()

	t.Run("attach", func(t *testing.T) {
		t.Parallel()
		eng := newFakeEngine()
		eng.attachErr = errors.New("no attach")
		_, err := start(t.Context(), eng, validGroup())
		require.ErrorContains(t, err, "attach to container")
		require.Equal(t, []string{"exists", "create", "wait-removed", "attach", "remove", "close"}, eng.callList())
	})
	t.Run("start", func(t *testing.T) {
		t.Parallel()
		eng := newFakeEngine()
		eng.startErr = errors.New("no start")
		_, err := start(t.Context(), eng, validGroup())
		require.ErrorContains(t, err, "start container")
		require.Equal(t, []string{"exists", "create", "wait-removed", "attach", "start", "remove", "close"}, eng.callList())
		_, closed := eng.att.state()
		require.True(t, closed, "attachment is closed when start fails")
	})
	t.Run("create", func(t *testing.T) {
		t.Parallel()
		eng := newFakeEngine()
		eng.createErr = errors.New("no create")
		_, err := start(t.Context(), eng, validGroup())
		require.ErrorContains(t, err, "create container")
		require.Equal(t, []string{"exists", "create", "close"}, eng.callList())
	})
}

func TestStart_RejectsInvalidGroup(t *testing.T) {
	t.Parallel()

	eng := newFakeEngine()
	g := validGroup()
	g.Main.Image = ""
	_, err := start(t.Context(), eng, g)
	require.Error(t, err)
	require.Equal(t, []string{"close"}, eng.callList())
}

func TestSession_TransportCarriesStdoutAndStdin(t *testing.T) {
	t.Parallel()

	eng := newFakeEngine()
	s, err := start(t.Context(), eng, validGroup())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	tr, ok := s.Transport().(*mcp.IOTransport)
	require.True(t, ok)

	go func() {
		_, _ = eng.att.in.Write(frame(1, `{"id":1}`+"\n"))
		_, _ = eng.att.in.Write(frame(2, "warning on stderr\n"))
		_, _ = eng.att.in.Write(frame(1, `{"id":2}`+"\n"))
	}()

	r := bufio.NewReader(tr.Reader)
	line, err := r.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, `{"id":1}`+"\n", line)
	line, err = r.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, `{"id":2}`+"\n", line, "stderr frames never reach the MCP stream")

	_, err = tr.Writer.Write([]byte(`{"hello":true}` + "\n"))
	require.NoError(t, err)
	require.Equal(t, `{"hello":true}`+"\n", eng.att.writtenString())

	require.NoError(t, tr.Writer.Close())
	closedWrite, _ := eng.att.state()
	require.True(t, closedWrite, "closing the writer closes the container's stdin")
}

func TestSession_ExitErrIncludesCodeAndStderrTail(t *testing.T) {
	t.Parallel()

	eng := newFakeEngine()
	s, err := start(t.Context(), eng, validGroup())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	tr := s.Transport().(*mcp.IOTransport)

	eng.att.feed(t, frame(2, "panic: bad config\n"))
	require.NoError(t, eng.att.in.Close())
	_, err = io.ReadAll(tr.Reader)
	require.NoError(t, err)

	eng.exit <- exitResult{Code: 3}

	exitErr := s.ExitErr()
	require.Error(t, exitErr)
	require.ErrorContains(t, exitErr, "code 3")
	require.ErrorContains(t, exitErr, "panic: bad config")
}

func TestSession_ExitErrIsNilWhileRunning(t *testing.T) {
	t.Parallel()

	eng := newFakeEngine()
	s, err := start(t.Context(), eng, validGroup())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	done := make(chan error, 1)
	go func() { done <- s.ExitErr() }()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(exitWait + 3*time.Second):
		t.Fatal("ExitErr blocked")
	}
}

func TestSession_CloseStopsViaStdinWithoutKilling(t *testing.T) {
	t.Parallel()

	eng := newFakeEngine()
	s, err := start(t.Context(), eng, validGroup())
	require.NoError(t, err)

	require.NoError(t, s.Close())
	require.NoError(t, s.Close(), "Close is idempotent")

	calls := eng.callList()
	require.NotContains(t, calls, "kill")
	require.Equal(t, 1, count(calls, "remove"))
	require.Equal(t, 1, count(calls, "close"))
	closedWrite, closed := eng.att.state()
	require.True(t, closedWrite)
	require.True(t, closed)
}

func TestSession_CloseKillsAContainerThatIgnoresStdin(t *testing.T) {
	old := stdinGrace
	stdinGrace = 50 * time.Millisecond
	t.Cleanup(func() { stdinGrace = old })

	eng := newFakeEngine()
	eng.att.onCloseW = nil
	s, err := start(t.Context(), eng, validGroup())
	require.NoError(t, err)

	require.NoError(t, s.Close())

	calls := eng.callList()
	require.Equal(t, 1, count(calls, "kill"))
	require.Equal(t, 1, count(calls, "remove"))
	require.Equal(t, 1, count(calls, "close"))
}

func TestSession_ClosingTheReaderCleansUp(t *testing.T) {
	t.Parallel()

	eng := newFakeEngine()
	s, err := start(t.Context(), eng, validGroup())
	require.NoError(t, err)

	tr := s.Transport().(*mcp.IOTransport)
	require.NoError(t, tr.Reader.Close())

	require.Equal(t, 1, count(eng.callList(), "remove"))
}

func TestStderrSink(t *testing.T) {
	t.Parallel()

	s := &stderrSink{server: "srv"}
	_, err := s.Write([]byte("first line\nsecond "))
	require.NoError(t, err)
	_, err = s.Write([]byte("line\r\n"))
	require.NoError(t, err)
	require.Equal(t, "first line\nsecond line", s.Tail())

	big := bytes.Repeat([]byte("x"), stderrTailSize*2)
	_, err = s.Write(append(big, "END"...))
	require.NoError(t, err)
	require.Len(t, s.Tail(), stderrTailSize)
	require.Equal(t, "END", s.Tail()[stderrTailSize-3:])
}

func count(list []string, want string) int {
	n := 0
	for _, v := range list {
		if v == want {
			n++
		}
	}
	return n
}
