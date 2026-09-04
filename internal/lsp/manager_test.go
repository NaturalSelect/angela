package lsp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/csync"
	powernapconfig "github.com/charmbracelet/x/powernap/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestUnavailableBackoff(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 3, 26, 0, 0, 0, 0, time.UTC)
	now := base

	manager := &Manager{
		unavailable: csync.NewMap[string, time.Time](),
		now:         func() time.Time { return now },
	}

	require.False(t, manager.recentlyUnavailable("gopls"))

	manager.markUnavailable("gopls")
	require.True(t, manager.recentlyUnavailable("gopls"))

	now = now.Add(unavailableRetryDelay + time.Second)
	require.False(t, manager.recentlyUnavailable("gopls"))
	_, exists := manager.unavailable.Get("gopls")
	require.False(t, exists)

	manager.markUnavailable("gopls")
	manager.clearUnavailable("gopls")
	require.False(t, manager.recentlyUnavailable("gopls"))
}

func TestCanAutoStartFiltersBeforeLookingUpCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		server  *powernapconfig.ServerConfig
		want    bool
		lookups int
	}{
		{
			name: "unhandled file type",
			server: &powernapconfig.ServerConfig{
				Command:   "typescript-language-server",
				FileTypes: []string{"typescript"},
			},
		},
		{
			name: "generic command",
			server: &powernapconfig.ServerConfig{
				Command:   "node",
				FileTypes: []string{"go"},
			},
		},
		{
			name: "handled file type",
			server: &powernapconfig.ServerConfig{
				Command:   "gopls",
				FileTypes: []string{"go"},
			},
			want:    true,
			lookups: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lookups := 0
			manager := &Manager{
				unavailable: csync.NewMap[string, time.Time](),
				now:         time.Now,
				lookPath: func(string) (string, error) {
					lookups++
					return "/usr/local/bin/gopls", nil
				},
			}

			got := manager.canAutoStart("test", "main.go", t.TempDir(), tt.server)

			require.Equal(t, tt.want, got)
			require.Equal(t, tt.lookups, lookups)
		})
	}
}

func TestCanAutoStartCachesMissingCommand(t *testing.T) {
	t.Parallel()

	lookups := 0
	manager := &Manager{
		unavailable: csync.NewMap[string, time.Time](),
		now:         time.Now,
		lookPath: func(string) (string, error) {
			lookups++
			return "", errors.New("not found")
		},
	}
	server := &powernapconfig.ServerConfig{
		Command:   "gopls",
		FileTypes: []string{"go"},
	}

	require.False(t, manager.canAutoStart("gopls", "main.go", t.TempDir(), server))
	require.False(t, manager.canAutoStart("gopls", "main.go", t.TempDir(), server))
	require.Equal(t, 1, lookups)
}

func TestNewManager(t *testing.T) {
	t.Parallel()

	t.Run("loads default servers and exposes an empty client map", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewTestStore(&config.Config{Options: &config.Options{}})
		mgr := NewManager(cfg)

		require.NotNil(t, mgr.Clients())
		require.Equal(t, 0, mgr.Clients().Len())
		_, ok := mgr.manager.GetServer("gopls")
		require.True(t, ok, "default servers should be loaded, including gopls")
	})

	t.Run("disabled user config removes the default server", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewTestStore(&config.Config{
			Options: &config.Options{},
			LSP: config.LSPs{
				"gopls": {Disabled: true},
			},
		})
		mgr := NewManager(cfg)

		_, ok := mgr.manager.GetServer("gopls")
		require.False(t, ok, "a disabled user config must remove the default server")
	})

	t.Run("default no-op callback does not panic", func(t *testing.T) {
		t.Parallel()
		cfg := config.NewTestStore(&config.Config{Options: &config.Options{}})
		mgr := NewManager(cfg)
		mgr.callback("anything", nil)
	})
}

func TestManagerClients(t *testing.T) {
	t.Parallel()

	mgr := &Manager{clients: csync.NewMap[string, *Client]()}
	require.NotNil(t, mgr.Clients())

	mgr.clients.Set("gopls", &Client{name: "gopls"})
	require.Equal(t, 1, mgr.Clients().Len())
}

func TestSetCallback(t *testing.T) {
	t.Parallel()

	mgr := &Manager{callback: func(string, *Client) {}}

	var gotName string
	var gotClient *Client
	mgr.SetCallback(func(name string, client *Client) {
		gotName = name
		gotClient = client
	})

	want := &Client{name: "gopls"}
	mgr.callback("gopls", want)
	require.Equal(t, "gopls", gotName)
	require.Same(t, want, gotClient)
}

func TestTrackConfigured(t *testing.T) {
	t.Parallel()

	pnMgr := powernapconfig.NewManager()
	pnMgr.AddServer("configured-one", &powernapconfig.ServerConfig{Command: "one"})
	pnMgr.AddServer("not-configured", &powernapconfig.ServerConfig{Command: "two"})

	cfg := config.NewTestStore(&config.Config{
		Options: &config.Options{},
		LSP: config.LSPs{
			"configured-one": {Command: "one"},
		},
	})

	var mu sync.Mutex
	var calls []string
	mgr := &Manager{
		manager: pnMgr,
		cfg:     cfg,
		callback: func(name string, client *Client) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, name)
			require.Nil(t, client)
		},
	}

	mgr.TrackConfigured(context.Background())

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"configured-one"}, calls)
}

func TestStart_NoopOutsideWorkingDir(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{Options: &config.Options{}})
	mgr := NewManager(cfg)

	// NewTestStore never sets a working directory, so every absolute path
	// falls outside it and Start is always a no-op: no servers are ever
	// looked up or spawned.
	mgr.Start(context.Background(), "/some/absolute/path/main.go")

	require.Equal(t, 0, mgr.Clients().Len())
}

func TestIsUserConfigured(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{
		Options: &config.Options{},
		LSP: config.LSPs{
			"enabled":  {Command: "gopls"},
			"disabled": {Command: "rust-analyzer", Disabled: true},
		},
	})
	mgr := &Manager{cfg: cfg}

	require.True(t, mgr.isUserConfigured("enabled"))
	require.False(t, mgr.isUserConfigured("disabled"))
	require.False(t, mgr.isUserConfigured("missing"))
}

func TestBuildConfig(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{
		Options: &config.Options{},
		LSP: config.LSPs{
			"gopls": {Timeout: 42},
		},
	})
	mgr := &Manager{cfg: cfg}

	server := &powernapconfig.ServerConfig{
		Command:     "gopls",
		Args:        []string{"serve"},
		Environment: map[string]string{"FOO": "bar"},
		FileTypes:   []string{"go"},
		RootMarkers: []string{"go.mod"},
		InitOptions: map[string]any{"a": 1},
		Settings:    map[string]any{"b": 2},
	}

	got := mgr.buildConfig("gopls", server)
	require.Equal(t, "gopls", got.Command)
	require.Equal(t, []string{"serve"}, got.Args)
	require.Equal(t, map[string]string{"FOO": "bar"}, got.Env)
	require.Equal(t, []string{"go"}, got.FileTypes)
	require.Equal(t, []string{"go.mod"}, got.RootMarkers)
	require.Equal(t, map[string]any{"a": 1}, got.InitOptions)
	require.Equal(t, map[string]any{"b": 2}, got.Options)
	require.Equal(t, 42, got.Timeout)

	// A server with no matching user config gets no timeout override.
	gotNoUser := mgr.buildConfig("unknown-server", server)
	require.Equal(t, 0, gotNoUser.Timeout)
}

func TestResolveServerName(t *testing.T) {
	t.Parallel()

	pnMgr := powernapconfig.NewManager()
	pnMgr.AddServer("gopls", &powernapconfig.ServerConfig{Command: "custom-gopls-binary"})

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"exact name match returns unchanged", "gopls", "gopls"},
		{"command alias resolves to the registered name", "custom-gopls-binary", "gopls"},
		{"unknown name falls back unchanged", "totally-unknown", "totally-unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, resolveServerName(pnMgr, tt.in))
		})
	}
}

func TestHandlesFiletype(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		fileTypes []string
		path      string
		want      bool
	}{
		{"no filetypes handles everything", nil, "main.go", true},
		{"bare suffix match", []string{"go"}, "main.go", true},
		{"dotted suffix match", []string{".go"}, "main.go", true},
		{"case-insensitive match", []string{"GO"}, "main.go", true},
		{"no match", []string{"py"}, "main.go", false},
		{"language kind match when suffix does not match", []string{"dockerfile"}, "/some/dir/Dockerfile", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, handlesFiletype("test", tt.fileTypes, tt.path))
		})
	}
}

func TestHasRootMarkers(t *testing.T) {
	t.Parallel()

	t.Run("no markers always matches", func(t *testing.T) {
		t.Parallel()
		require.True(t, hasRootMarkers(t.TempDir(), nil))
	})

	t.Run("marker file present", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644))
		require.True(t, hasRootMarkers(dir, []string{"go.mod"}))
	})

	t.Run("marker file absent", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.False(t, hasRootMarkers(dir, []string{"go.mod", "Cargo.toml"}))
	})
}

func TestHandles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644))

	server := &powernapconfig.ServerConfig{
		Command:     "gopls",
		FileTypes:   []string{"go"},
		RootMarkers: []string{"go.mod"},
	}

	require.True(t, handles(server, filepath.Join(dir, "main.go"), dir))
	require.False(t, handles(server, filepath.Join(dir, "main.py"), dir), "wrong file type")

	noMarkerDir := t.TempDir()
	require.False(t, handles(server, filepath.Join(noMarkerDir, "main.go"), noMarkerDir), "missing root marker")
}

func TestStartServer_ExistingReadyClient(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{Options: &config.Options{}})
	existing := newTestClient()
	existing.SetServerState(StateReady)

	var calls []string
	mgr := &Manager{
		clients: csync.NewMap[string, *Client](),
		cfg:     cfg,
		callback: func(name string, client *Client) {
			calls = append(calls, name)
			require.Same(t, existing, client)
		},
	}
	mgr.clients.Set("gopls", existing)

	server := &powernapconfig.ServerConfig{Command: "gopls", FileTypes: []string{"go"}}
	mgr.startServer("gopls", "main.go", server)

	require.Equal(t, []string{"gopls"}, calls)
	got, ok := mgr.clients.Get("gopls")
	require.True(t, ok)
	require.Same(t, existing, got, "an already-ready client must not be replaced")
}

func TestStartServer_UserConfiguredButDoesNotHandleFile(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{
		Options: &config.Options{},
		LSP: config.LSPs{
			"gopls": {Command: "gopls", FileTypes: []string{"go"}},
		},
	})
	mgr := &Manager{
		clients:  csync.NewMap[string, *Client](),
		cfg:      cfg,
		callback: func(string, *Client) {},
	}

	server := &powernapconfig.ServerConfig{Command: "gopls", FileTypes: []string{"go"}}
	// A .py file never matches a Go-only server, so startServer must
	// return before ever attempting to create a client.
	mgr.startServer("gopls", "main.py", server)

	require.Equal(t, 0, mgr.clients.Len())
}

func TestStartServer_AutoLSPDisabled(t *testing.T) {
	t.Parallel()

	autoLSP := false
	cfg := config.NewTestStore(&config.Config{
		Options: &config.Options{AutoLSP: &autoLSP},
	})
	mgr := &Manager{
		clients:  csync.NewMap[string, *Client](),
		cfg:      cfg,
		callback: func(string, *Client) {},
	}

	server := &powernapconfig.ServerConfig{Command: "gopls", FileTypes: []string{"go"}}
	mgr.startServer("gopls", "main.go", server)

	require.Equal(t, 0, mgr.clients.Len())
}

// TestStartServer_FullFlow_InitializeFailsFast drives startServer all the
// way through creating a real client. "echo" exits almost immediately, so
// the LSP initialize handshake fails fast with a closed-connection error
// (see newRealClient in client_test.go) instead of needing a real language
// server or waiting out the 30s default init timeout.
func TestStartServer_FullFlow_InitializeFailsFast(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{
		Options: &config.Options{},
		LSP: config.LSPs{
			"echo-lsp": {Command: "echo", FileTypes: []string{"go"}},
		},
	})

	var mu sync.Mutex
	var calls []string
	mgr := &Manager{
		clients: csync.NewMap[string, *Client](),
		cfg:     cfg,
		callback: func(name string, client *Client) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, name)
			require.NotNil(t, client)
		},
	}

	server := &powernapconfig.ServerConfig{Command: "echo", Args: []string{"hi"}, FileTypes: []string{"go"}}
	mgr.startServer("echo-lsp", "main.go", server)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"echo-lsp"}, calls)
	require.Equal(t, 0, mgr.clients.Len(), "a client that fails to initialize is removed")
}

func TestKillAll(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{Options: &config.Options{}})
	c1 := newRealClient(t, t.TempDir(), nil)
	c2 := newRealClient(t, t.TempDir(), nil)

	var mu sync.Mutex
	var calls []string
	mgr := &Manager{
		clients: csync.NewMap[string, *Client](),
		cfg:     cfg,
		callback: func(name string, _ *Client) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, name)
		},
	}
	mgr.clients.Set("a", c1)
	mgr.clients.Set("b", c2)

	mgr.KillAll(context.Background())

	require.Equal(t, 0, mgr.clients.Len())
	mu.Lock()
	defer mu.Unlock()
	require.ElementsMatch(t, []string{"a", "b"}, calls)
	require.Equal(t, StateStopped, c1.GetServerState())
	require.Equal(t, StateStopped, c2.GetServerState())
}

func TestStopAll(t *testing.T) {
	t.Parallel()

	cfg := config.NewTestStore(&config.Config{Options: &config.Options{}})
	c1 := newRealClient(t, t.TempDir(), nil)

	var mu sync.Mutex
	var calls []string
	mgr := &Manager{
		clients: csync.NewMap[string, *Client](),
		cfg:     cfg,
		callback: func(name string, _ *Client) {
			mu.Lock()
			defer mu.Unlock()
			calls = append(calls, name)
		},
	}
	mgr.clients.Set("a", c1)

	mgr.StopAll(context.Background())

	require.Equal(t, 0, mgr.clients.Len())
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"a"}, calls)
	require.Equal(t, StateStopped, c1.GetServerState())
}
