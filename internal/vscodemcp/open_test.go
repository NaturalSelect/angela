package vscodemcp_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/vscodemcp"
	"github.com/stretchr/testify/require"
)

// TestHelperProcess is not a real test. Invoked as a subprocess with
// ANGELA_VSCODEMCP_HELPER_OUT set, it stands in for the VS Code CLI so
// Open can be tested without VS Code installed: it records the
// arguments it received as JSON to that path, or exits non-zero when
// ANGELA_VSCODEMCP_HELPER_FAIL is set.
func TestHelperProcess(t *testing.T) {
	out := os.Getenv("ANGELA_VSCODEMCP_HELPER_OUT")
	if out == "" {
		return
	}

	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) > 0 {
		args = args[1:]
	}

	if os.Getenv("ANGELA_VSCODEMCP_HELPER_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "boom")
		os.Exit(1)
	}

	data, err := json.Marshal(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := os.WriteFile(out, data, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}

// fakeCode returns a Client whose CLI is the current test binary running
// TestHelperProcess, and the file the helper records its arguments to.
func fakeCode(t *testing.T, fail bool) (*vscodemcp.Client, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "args.json")
	t.Setenv("ANGELA_VSCODEMCP_HELPER_OUT", out)
	if fail {
		t.Setenv("ANGELA_VSCODEMCP_HELPER_FAIL", "1")
	}
	return &vscodemcp.Client{Command: []string{os.Args[0], "-test.run=TestHelperProcess", "--"}}, out
}

func TestClient_Open(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "foo.go")
	relAbs, err := filepath.Abs("rel.go")
	require.NoError(t, err)
	dashAbs, err := filepath.Abs("-n")
	require.NoError(t, err)

	tests := []struct {
		name string
		req  vscodemcp.OpenRequest
		want string
	}{
		{name: "path only", req: vscodemcp.OpenRequest{Path: abs}, want: abs},
		{name: "line", req: vscodemcp.OpenRequest{Path: abs, Line: 12}, want: abs + ":12"},
		{name: "line and column", req: vscodemcp.OpenRequest{Path: abs, Line: 12, Column: 3}, want: abs + ":12:3"},
		{name: "column without line is ignored", req: vscodemcp.OpenRequest{Path: abs, Column: 3}, want: abs},
		{name: "relative path is made absolute", req: vscodemcp.OpenRequest{Path: "rel.go"}, want: relAbs},
		{name: "leading dash cannot become a flag", req: vscodemcp.OpenRequest{Path: "-n"}, want: dashAbs},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, out := fakeCode(t, false)

			require.NoError(t, client.Open(t.Context(), tt.req))

			data, err := os.ReadFile(out)
			require.NoError(t, err)
			var args []string
			require.NoError(t, json.Unmarshal(data, &args))
			require.Equal(t, []string{"--reuse-window", "--goto", tt.want}, args)
		})
	}
}

func TestClient_Open_EmptyPath(t *testing.T) {
	client, _ := fakeCode(t, false)

	require.Error(t, client.Open(t.Context(), vscodemcp.OpenRequest{}))
}

func TestClient_Open_CLIFailureReportsStderr(t *testing.T) {
	client, _ := fakeCode(t, true)

	err := client.Open(t.Context(), vscodemcp.OpenRequest{Path: filepath.Join(t.TempDir(), "foo.go")})
	require.ErrorContains(t, err, "boom")
}

func TestClient_Open_MissingCLI(t *testing.T) {
	client := &vscodemcp.Client{Command: []string{"angela-vscodemcp-does-not-exist"}}

	require.Error(t, client.Open(t.Context(), vscodemcp.OpenRequest{Path: filepath.Join(t.TempDir(), "foo.go")}))
}
