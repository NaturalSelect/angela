package permission

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/filepathext"
	"github.com/stretchr/testify/require"
)

// linkedWorkspace builds a workspace holding `escape`, a symlink
// pointing at a directory outside it, plus a secret file in that
// outside directory.
func linkedWorkspace(t *testing.T) (workspace, secret string) {
	t.Helper()

	root := t.TempDir()
	workspace = filepath.Join(root, "workspace")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))

	secret = filepath.Join(outside, "id_rsa")
	require.NoError(t, os.WriteFile(secret, []byte("key"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(workspace, "escape")))

	return workspace, secret
}

// TestSymlinkEscapeFromWorkspaceBlocked pins the hole a lexical scope
// check leaves open: a link inside the workspace pointing out of it
// makes an outside file look like an inside one, and reads inside the
// workspace are auto-approved. The read must still reach the user.
func TestSymlinkEscapeFromWorkspaceBlocked(t *testing.T) {
	t.Parallel()

	workspace, _ := linkedWorkspace(t)
	through := filepath.Join(workspace, "escape", "id_rsa")

	t.Run("a tool reading through the link is not auto-allowed", func(t *testing.T) {
		t.Parallel()
		svc := NewPermissionService(workspace, false, nil).(*permissionService)

		_, ok := svc.withinScope(Access{
			Tool: "view", Action: ActionRead, Path: through,
		})
		require.False(t, ok, "reading outside the workspace through a link must ask")
	})

	t.Run("a command reading through the link is not auto-allowed", func(t *testing.T) {
		t.Parallel()
		svc := NewPermissionService(workspace, false, nil).(*permissionService)

		_, ok := svc.withinScope(Access{
			Tool: "bash", Action: ActionExecute,
			Command: "cat escape/id_rsa", Path: workspace,
		})
		require.False(t, ok, "cat through a link must ask, like view does")
	})

	t.Run("a genuine file inside the workspace still reads freely", func(t *testing.T) {
		t.Parallel()
		svc := NewPermissionService(workspace, false, nil).(*permissionService)

		inside := filepath.Join(workspace, "main.go")
		require.NoError(t, os.WriteFile(inside, []byte("package main"), 0o644))

		_, ok := svc.withinScope(Access{
			Tool: "view", Action: ActionRead, Path: inside,
		})
		require.True(t, ok, "the workspace is what Angela was pointed at")
	})
}

// TestSymlinkEscapeReachesTheGate pins the same escape through the real
// entry point, so the protection cannot be lost by the ladder skipping
// the scope check.
func TestSymlinkEscapeReachesTheGate(t *testing.T) {
	t.Parallel()

	workspace, _ := linkedWorkspace(t)
	svc := NewPermissionService(workspace, false, nil)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	requests := svc.Subscribe(ctx)
	done := make(chan Decision, 1)
	go func() {
		done <- svc.Gate(ctx, GateRequest{
			SessionID: "s", ToolCallID: "c",
			Access: Access{
				Tool: "view", Action: ActionRead,
				Path: filepath.Join(workspace, "escape", "id_rsa"),
			},
		})
	}()

	select {
	case event := <-requests:
		// The user was asked, which is the whole point.
		svc.Deny(event.Payload)
	case <-t.Context().Done():
		t.Fatal("the gate never asked; the escape was auto-approved")
	}

	require.Equal(t, OutcomeUserDeny, (<-done).Outcome)
}

// TestResolvePathKeepsUncreatedFiles pins that a file that does not
// exist yet still resolves through its real parent, which is what a
// write to a new file needs.
func TestResolvePathKeepsUncreatedFiles(t *testing.T) {
	t.Parallel()

	workspace, _ := linkedWorkspace(t)

	t.Run("a new file under a link resolves outside", func(t *testing.T) {
		t.Parallel()
		resolved := resolvePath(filepath.Join(workspace, "escape", "new.txt"), workspace)
		require.False(t, withinResolvedDir(resolved, resolvePath(workspace, "")),
			"writing a new file through a link lands outside the workspace")
	})

	t.Run("a new file under a real directory stays inside", func(t *testing.T) {
		t.Parallel()
		resolved := resolvePath(filepath.Join(workspace, "sub", "new.txt"), workspace)
		require.True(t, withinResolvedDir(resolved, resolvePath(workspace, "")))
	})

	t.Run("a relative path resolves against the working directory", func(t *testing.T) {
		t.Parallel()
		require.Equal(t,
			resolvePath(filepath.Join(workspace, "a.go"), ""),
			resolvePath("a.go", workspace))
	})
}

// TestScopeAgreesWithHowToolsOpenFiles pins that the scope check anchors
// a path where the tool actually opens it. Tools reach their files
// through filepathext.SmartJoin, which on Windows treats a leading
// slash as absolute even without a volume letter. Anchoring such a path
// to the workspace here would make an outside file look like an inside
// one, and inside reads are auto-approved.
//
// Only the Windows branch of SmartIsAbs differs from filepath.IsAbs, so
// this guard does its real work in CI rather than on Linux.
func TestScopeAgreesWithHowToolsOpenFiles(t *testing.T) {
	t.Parallel()

	workspace, _ := linkedWorkspace(t)
	rooted := filepath.FromSlash("/etc/passwd")

	t.Run("the working directory does not move a rooted path", func(t *testing.T) {
		t.Parallel()
		require.Equal(t,
			resolvePath(filepathext.SmartJoin(workspace, rooted), ""),
			resolvePath(rooted, workspace),
			"the scope check must resolve the path the tool opens")
	})

	t.Run("a rooted path outside the workspace is out of scope", func(t *testing.T) {
		t.Parallel()
		svc := NewPermissionService(workspace, false, nil).(*permissionService)

		_, ok := svc.withinScope(Access{
			Tool: "view", Action: ActionRead, Path: rooted,
		})
		require.False(t, ok, "reading a rooted path outside the workspace must ask")
	})
}

// TestContainmentComparesWholeComponents pins that the inside/outside
// question is answered on path components rather than on the first two
// characters of the relative path. Both directions matter: a sibling
// directory whose name merely starts where the workspace name ends is
// outside, and a directory legitimately named "..foo" is inside.
func TestContainmentComparesWholeComponents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	sibling := filepath.Join(root, "workspace-copy")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	require.NoError(t, os.MkdirAll(sibling, 0o755))

	t.Run("a sibling sharing the name prefix is outside", func(t *testing.T) {
		t.Parallel()
		svc := NewPermissionService(workspace, false, nil).(*permissionService)

		_, ok := svc.withinScope(Access{
			Tool: "view", Action: ActionRead,
			Path: filepath.Join(sibling, "secret"),
		})
		require.False(t, ok, "workspace-copy is not inside workspace")
	})

	t.Run("a directory named ..foo is inside", func(t *testing.T) {
		t.Parallel()
		dotted := filepath.Join(workspace, "..foo")
		require.NoError(t, os.MkdirAll(dotted, 0o755))
		svc := NewPermissionService(workspace, false, nil).(*permissionService)

		_, ok := svc.withinScope(Access{
			Tool: "view", Action: ActionRead,
			Path: filepath.Join(dotted, "main.go"),
		})
		require.True(t, ok,
			"..foo is an ordinary directory inside the workspace, not a way out of it")
	})

	t.Run("the parent directory is outside", func(t *testing.T) {
		t.Parallel()
		svc := NewPermissionService(workspace, false, nil).(*permissionService)

		_, ok := svc.withinScope(Access{
			Tool: "view", Action: ActionRead,
			Path: filepath.Join(root, "secret"),
		})
		require.False(t, ok)
	})
}
