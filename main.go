// Package main is the entry point for the Angela CLI.
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
	// NOTE: if we enter by a sub process with the appropriate sandboxing and network restrictions
	// the main process will not continue past this point.
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
