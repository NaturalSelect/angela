package editorapproval

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestHelperProcess is not a real test. Invoked as a subprocess with
// ANGELA_EDITORAPPROVAL_HELPER=1 set, it behaves like a stand-in for
// the VS Code CLI so Review can be tested without VS Code installed:
// it reads the `--wait --diff <old> <new>` paths and rewrites the
// "after" file per ANGELA_EDITORAPPROVAL_HELPER_MODE, simulating what a
// user would do in the editor before closing it.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("ANGELA_EDITORAPPROVAL_HELPER") != "1" {
		return
	}

	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) > 0 {
		args = args[1:]
	}
	if len(args) != 4 || args[0] != "--wait" || args[1] != "--diff" {
		fmt.Fprintf(os.Stderr, "unexpected helper args: %v\n", args)
		os.Exit(2)
	}
	oldPath, newPath := args[2], args[3]

	switch os.Getenv("ANGELA_EDITORAPPROVAL_HELPER_MODE") {
	case "edit":
		content := os.Getenv("ANGELA_EDITORAPPROVAL_HELPER_CONTENT")
		if err := os.WriteFile(newPath, []byte(content), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	case "revert":
		old, err := os.ReadFile(oldPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if err := os.WriteFile(newPath, old, 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	case "fail":
		os.Exit(1)
	}
}

// fakeVSCode returns a VSCode that re-invokes the current test binary
// as TestHelperProcess instead of running the real `code` CLI, so
// Review's behavior can be exercised without VS Code installed.
func fakeVSCode(t *testing.T, mode, content string) VSCode {
	t.Helper()
	t.Setenv("ANGELA_EDITORAPPROVAL_HELPER", "1")
	t.Setenv("ANGELA_EDITORAPPROVAL_HELPER_MODE", mode)
	t.Setenv("ANGELA_EDITORAPPROVAL_HELPER_CONTENT", content)
	return VSCode{Command: []string{os.Args[0], "-test.run=TestHelperProcess", "--"}}
}

func TestVSCode_Review_ApproveUnchanged(t *testing.T) {
	v := fakeVSCode(t, "", "")
	require.Equal(t, "vscode", v.Name())

	got, err := v.Review(t.Context(), Request{
		FilePath:   "foo.go",
		OldContent: "old\n",
		NewContent: "new\n",
	})
	require.NoError(t, err)
	require.Equal(t, Decision{Outcome: OutcomeApprove, Content: "new\n"}, got)
}

func TestVSCode_Review_ApproveEdited(t *testing.T) {
	v := fakeVSCode(t, "edit", "edited\n")

	got, err := v.Review(t.Context(), Request{
		FilePath:   "foo.go",
		OldContent: "old\n",
		NewContent: "new\n",
	})
	require.NoError(t, err)
	require.Equal(t, Decision{Outcome: OutcomeApprove, Content: "edited\n"}, got)
}

func TestVSCode_Review_Deny(t *testing.T) {
	v := fakeVSCode(t, "revert", "")

	got, err := v.Review(t.Context(), Request{
		FilePath:   "foo.go",
		OldContent: "old\n",
		NewContent: "new\n",
	})
	require.NoError(t, err)
	require.Equal(t, Decision{
		Outcome: OutcomeDeny,
		Reason:  "reverted to the original content in the editor",
	}, got)
}

func TestVSCode_Review_ProcessError(t *testing.T) {
	v := fakeVSCode(t, "fail", "")

	_, err := v.Review(t.Context(), Request{
		FilePath:   "foo.go",
		OldContent: "old\n",
		NewContent: "new\n",
	})
	require.Error(t, err)
}

func TestVSCode_Available(t *testing.T) {
	require.True(t, VSCode{Command: []string{os.Args[0]}}.Available())
	require.False(t, VSCode{Command: []string{"angela-editorapproval-does-not-exist"}}.Available())
}
