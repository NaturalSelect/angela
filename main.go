// Package main is the entry point for the Angela CLI.
//
//	@title			Angela API
//	@version		1.0
//	@description	Angela is a terminal-based AI coding assistant. This API is served over a Unix socket (or Windows named pipe) and provides programmatic access to workspaces, sessions, agents, LSP, MCP, and more.
//	@contact.name	Charm
//	@contact.url	https://charm.sh
//	@license.name	MIT
//	@license.url	https://github.com/NaturalSelect/angela/blob/main/LICENSE
//	@BasePath		/v1
package main

import (
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"

	"github.com/NaturalSelect/angela/internal/cmd"
	_ "github.com/NaturalSelect/angela/internal/dns"
	"github.com/NaturalSelect/angela/internal/sandbox"
	_ "github.com/joho/godotenv/autoload"
)

func main() {
	// Must run before anything else: when Angela relaunches itself to
	// restrict a sandboxed command's network access (see
	// sandbox.WrapForChildNetworkRestriction), this process is that
	// relaunch, not a normal Angela invocation, and never returns.
	sandbox.RunChildExecLauncherIfRequested(os.Args)

	if os.Getenv("ANGELA_PROFILE") != "" {
		go func() {
			slog.Info("Serving pprof at localhost:6060")
			if httpErr := http.ListenAndServe("localhost:6060", nil); httpErr != nil {
				slog.Error("Failed to pprof listen", "error", httpErr)
			}
		}()
	}

	cmd.Execute()
}
