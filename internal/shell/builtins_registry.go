package shell

import (
	"context"
	"io"
)

// BuiltinHandler is a function that handles a shell builtin command.
// It receives the full args slice (including the command name as args[0]),
// the context, and I/O streams.
type BuiltinHandler func(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error

// builtins holds all in-process builtin handlers. Dispatched by
// builtinHandler in run.go.
var builtins = make(map[string]BuiltinHandler)

func init() {
	RegisterBuiltin("jq", handleJQ)
}

// RegisterBuiltin registers a builtin command handler. Must be called during
// init(), before any shell execution. If a handler is already registered for
// the same name, the new one replaces it.
func RegisterBuiltin(name string, handler BuiltinHandler) {
	builtins[name] = handler
}
