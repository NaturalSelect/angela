package profiling

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func startTestEndpoint(t *testing.T, dir string, pid int) func() {
	t.Helper()
	stop, err := serve(dir, pid)
	require.NoError(t, err)
	t.Cleanup(stop)
	return stop
}

// shortDir keeps the socket path under the platform sun_path limit, which
// t.TempDir() can exceed on macOS.
func shortDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "pp")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func readAll(t *testing.T, rc io.ReadCloser) []byte {
	t.Helper()
	defer rc.Close()
	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	return data
}

func TestServeExportsSnapshotProfiles(t *testing.T) {
	t.Parallel()

	dir := shortDir(t)
	pid := os.Getpid()
	startTestEndpoint(t, dir, pid)

	tests := []struct {
		name    string
		profile string
		debug   int
		wantGz  bool
		wantSub string
	}{
		{name: "goroutine binary", profile: "goroutine", wantGz: true},
		{name: "goroutine text", profile: "goroutine", debug: 1, wantSub: "goroutine profile:"},
		{name: "goroutine full stacks", profile: "goroutine", debug: 2, wantSub: "goroutine "},
		{name: "heap binary", profile: "heap", wantGz: true},
		{name: "allocs text", profile: "allocs", debug: 1, wantSub: "heap profile:"},
		{name: "threadcreate binary", profile: "threadcreate", wantGz: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := Request{PID: pid, Profile: Profile{Name: tt.profile}, Debug: tt.debug}
			rc, err := open(t.Context(), dir, pid, req.url())
			require.NoError(t, err)
			data := readAll(t, rc)

			if tt.wantGz {
				require.GreaterOrEqual(t, len(data), 2)
				require.Equal(t, []byte{0x1f, 0x8b}, data[:2], "binary profiles are gzip-compressed protobuf")
				return
			}
			require.Contains(t, string(data), tt.wantSub)
		})
	}
}

// NOTE: Not parallel. The CPU profiler and execution tracer are process-wide,
// so tests recording them must not overlap with each other.
func TestServeRecordsTimedProfiles(t *testing.T) {
	dir := shortDir(t)
	pid := os.Getpid()
	startTestEndpoint(t, dir, pid)

	for _, name := range []string{"cpu", "trace"} {
		t.Run(name, func(t *testing.T) {
			req := Request{PID: pid, Profile: Profile{Name: name, Timed: true}, Seconds: 1}
			start := time.Now()
			rc, err := open(t.Context(), dir, pid, req.url())
			require.NoError(t, err)
			data := readAll(t, rc)

			require.NotEmpty(t, data)
			require.GreaterOrEqual(t, time.Since(start), time.Second, "a timed profile records for the requested duration")
		})
	}
}

func TestCPUProfileMapsToPprofProfilePath(t *testing.T) {
	t.Parallel()

	req := Request{Profile: Profile{Name: "cpu", Timed: true}, Seconds: 7}
	require.Equal(t, "http://angela/debug/pprof/profile?seconds=7", req.url())
}

func TestServeRejectsUnknownProfile(t *testing.T) {
	t.Parallel()

	dir := shortDir(t)
	pid := os.Getpid()
	startTestEndpoint(t, dir, pid)

	_, err := open(t.Context(), dir, pid, endpointURL("nosuchprofile", nil))
	require.ErrorContains(t, err, http.StatusText(http.StatusNotFound))
}

func TestServeOnlyAnswersPprofPaths(t *testing.T) {
	t.Parallel()

	dir := shortDir(t)
	pid := os.Getpid()
	startTestEndpoint(t, dir, pid)

	for _, target := range []string{"http://angela/", "http://angela/debug/vars", "http://angela/secret"} {
		_, err := open(t.Context(), dir, pid, target)
		require.ErrorContains(t, err, http.StatusText(http.StatusNotFound), target)
	}
}

func TestServeSecuresRegistryDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits do not apply")
	}
	t.Parallel()

	dir := filepath.Join(shortDir(t), "reg")
	require.NoError(t, os.Mkdir(dir, 0o755))
	startTestEndpoint(t, dir, os.Getpid())

	info, err := os.Stat(dir)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestStopRemovesEndpoint(t *testing.T) {
	t.Parallel()

	dir := shortDir(t)
	pid := os.Getpid()
	stop := startTestEndpoint(t, dir, pid)

	procs, err := running(t.Context(), dir)
	require.NoError(t, err)
	require.Len(t, procs, 1)

	stop()
	stop()

	procs, err = running(t.Context(), dir)
	require.NoError(t, err)
	require.Empty(t, procs)
	require.NoFileExists(t, endpointPath(dir, pid))
}

func TestServeReplacesEndpointLeftByDeadProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipes leave no stale endpoint to replace")
	}
	t.Parallel()

	dir := shortDir(t)
	pid := os.Getpid()
	require.NoError(t, os.WriteFile(endpointPath(dir, pid), nil, 0o600))

	startTestEndpoint(t, dir, pid)

	procs, err := running(t.Context(), dir)
	require.NoError(t, err)
	require.Len(t, procs, 1)
}

func TestRunningReportsCmdline(t *testing.T) {
	t.Parallel()

	dir := shortDir(t)
	startTestEndpoint(t, dir, os.Getpid())

	procs, err := running(t.Context(), dir)
	require.NoError(t, err)
	require.Len(t, procs, 1)
	require.Equal(t, os.Getpid(), procs[0].PID)
	require.NotEmpty(t, procs[0].Cmdline)
}

func TestRunningWithoutRegistryDirectory(t *testing.T) {
	t.Parallel()

	procs, err := running(t.Context(), filepath.Join(t.TempDir(), "missing"))
	require.NoError(t, err)
	require.Empty(t, procs)
}

func TestRunningSkipsForeignFiles(t *testing.T) {
	t.Parallel()

	dir := shortDir(t)
	for _, name := range []string{"notes.txt", "abc.sock", "0.sock", "-5.sock"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o600))
	}

	procs, err := running(t.Context(), dir)
	require.NoError(t, err)
	require.Empty(t, procs)
	for _, name := range []string{"notes.txt", "abc.sock", "0.sock", "-5.sock"} {
		require.FileExists(t, filepath.Join(dir, name), "files that are not endpoints are left alone")
	}
}

func TestRunningRemovesStaleEndpoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipes leave no stale endpoint to remove")
	}
	t.Parallel()

	dir := shortDir(t)
	stale := endpointPath(dir, 4242)
	require.NoError(t, os.WriteFile(stale, nil, 0o600))

	procs, err := running(t.Context(), dir)
	require.NoError(t, err)
	require.Empty(t, procs)
	require.NoFileExists(t, stale)
}

func TestRunningIsSortedByPID(t *testing.T) {
	t.Parallel()

	dir := shortDir(t)
	// Several pids can be served from one test process because the
	// endpoint is keyed by the pid it is given.
	for _, pid := range []int{900003, 900001, 900002} {
		startTestEndpoint(t, dir, pid)
	}

	procs, err := running(t.Context(), dir)
	require.NoError(t, err)
	require.Len(t, procs, 3)
	require.Equal(t, []int{900001, 900002, 900003}, []int{procs[0].PID, procs[1].PID, procs[2].PID})
}

// NOTE: Not parallel, for the same reason as TestServeRecordsTimedProfiles.
func TestCancelledExportStopsRecording(t *testing.T) {
	dir := shortDir(t)
	pid := os.Getpid()
	startTestEndpoint(t, dir, pid)

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	req := Request{PID: pid, Profile: Profile{Name: "trace", Timed: true}, Seconds: 30}
	start := time.Now()
	rc, err := open(ctx, dir, pid, req.url())
	if err == nil {
		_, err = io.Copy(io.Discard, rc)
		_ = rc.Close()
	}
	require.Error(t, err)
	require.Less(t, time.Since(start), 10*time.Second)
}

func TestParseEndpointName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wantPID int
		wantOK  bool
	}{
		{"123.sock", 123, true},
		{"1.sock", 1, true},
		{"0.sock", 0, false},
		{"-1.sock", 0, false},
		{"12a.sock", 0, false},
		{".sock", 0, false},
		{"123", 0, false},
		{"123.sock.bak", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pid, ok := parseEndpointName(tt.name)
			require.Equal(t, tt.wantOK, ok)
			require.Equal(t, tt.wantPID, pid)
		})
	}
}

func TestRegistryDirKeepsSocketPathBindable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("named pipes have no path length limit")
	}

	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(string(filepath.Separator), "var", "a-very-long-runtime-directory", "that-keeps-going", "and-going", "and-going-more"))

	dir := registryDir()
	require.LessOrEqual(t, len(endpointPath(dir, longestPID)), maxSocketPathLen)
}
