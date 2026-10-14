package profiling

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	endpointHost = "angela"
	probeTimeout = 2 * time.Second
)

// Profile is a kind of runtime data that can be exported.
type Profile struct {
	Name  string
	Short string
	// Timed profiles are recorded over a duration instead of being a
	// snapshot of the current state.
	Timed bool
}

// DefaultSeconds is how long a timed profile records unless told otherwise.
const DefaultSeconds = 30

// Profiles lists every exportable profile.
var Profiles = []Profile{
	{Name: "goroutine", Short: "Stacks of all goroutines"},
	{Name: "heap", Short: "Live memory as of the last GC"},
	{Name: "allocs", Short: "All past allocations"},
	{Name: "threadcreate", Short: "Stacks that created OS threads"},
	{Name: "cpu", Short: "CPU time over a duration", Timed: true},
	{Name: "trace", Short: "Execution trace over a duration", Timed: true},
}

// Extension is the file extension of the profile in its binary format.
func (p Profile) Extension() string {
	if p.Name == "trace" {
		return "trace"
	}
	return "pb.gz"
}

// FileName names an export of p taken from process pid at the given time.
func (p Profile) FileName(pid int, at time.Time, extension string) string {
	return fmt.Sprintf("%s-%d-%s.%s", p.Name, pid, at.Format("20060102-150405"), extension)
}

// Request selects a profile of a serving process.
type Request struct {
	PID     int
	Profile Profile
	// Seconds is how long a timed profile records for.
	Seconds int
	// Debug selects the snapshot format: 0 is the binary format read by
	// `go tool pprof`, 1 is human-readable text, 2 is a full stack dump
	// for the goroutine profile.
	Debug int
}

func (r Request) url() string {
	name := r.Profile.Name
	query := url.Values{}
	switch {
	case name == "cpu":
		name = "profile"
		query.Set("seconds", strconv.Itoa(r.Seconds))
	case r.Profile.Timed:
		query.Set("seconds", strconv.Itoa(r.Seconds))
	case r.Debug > 0:
		query.Set("debug", strconv.Itoa(r.Debug))
	}
	return endpointURL(name, query)
}

func endpointURL(name string, query url.Values) string {
	u := url.URL{Scheme: "http", Host: endpointHost, Path: pprofPrefix + name, RawQuery: query.Encode()}
	return u.String()
}

// Process is an Angela process that serves profiles.
type Process struct {
	PID     int
	Cmdline string
}

// Running returns the processes serving profiles, ordered by pid. Endpoints
// left behind by processes that died without cleaning up are removed.
func Running(ctx context.Context) ([]Process, error) {
	return running(ctx, registryDir())
}

// Open starts exporting a profile. The caller must close the result, and
// keep ctx alive until it has read the whole profile.
func Open(ctx context.Context, req Request) (io.ReadCloser, error) {
	return open(ctx, registryDir(), req.PID, req.url())
}

func running(ctx context.Context, dir string) ([]Process, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read profile endpoint directory: %w", err)
	}

	var procs []Process
	for _, entry := range entries {
		pid, ok := parseEndpointName(entry.Name())
		if !ok {
			continue
		}
		cmdline, err := probe(ctx, dir, pid)
		if err != nil {
			if isStale(err) {
				_ = os.Remove(endpointPath(dir, pid))
			}
			continue
		}
		procs = append(procs, Process{PID: pid, Cmdline: cmdline})
	}
	slices.SortFunc(procs, func(a, b Process) int { return a.PID - b.PID })
	return procs, nil
}

func parseEndpointName(name string) (int, bool) {
	digits, ok := strings.CutSuffix(name, endpointExt)
	if !ok {
		return 0, false
	}
	pid, err := strconv.Atoi(digits)
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// probe also serves as the liveness check: only a running process answers.
func probe(ctx context.Context, dir string, pid int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	body, err := open(ctx, dir, pid, endpointURL("cmdline", nil))
	if err != nil {
		return "", err
	}
	defer body.Close()

	raw, err := io.ReadAll(io.LimitReader(body, 4096))
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(raw), "\x00", " "), nil
}

// isStale reports whether err means the endpoint file outlived its process.
// Dialing a file that is no longer a socket fails with ECONNREFUSED on Linux
// but with ENOTSOCK on the BSDs and macOS.
func isStale(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ENOTSOCK) ||
		errors.Is(err, fs.ErrNotExist)
}

func open(ctx context.Context, dir string, pid int, rawURL string) (io.ReadCloser, error) {
	client := &http.Client{Transport: &http.Transport{
		// Closing the body must close the connection, or a cancelled
		// export would keep recording on the serving side.
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialEndpoint(ctx, dir, pid)
		},
	}}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	return resp.Body, nil
}
