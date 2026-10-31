package acp

import (
	"context"
	"errors"
	"log/slog"

	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/NaturalSelect/angela/internal/commands"
)

// builtinCommand is a command the terminal UI has in its palette and that
// makes sense without a screen. run is called when the command is typed and
// returns either a prompt for the model or an action to carry out.
//
// NOTE: The rest of the palette is left out on purpose. Most of it is about
// the terminal itself, like scrolling, themes and dialogs. Undo reverts
// files and has no confirmation step in the protocol.
type builtinCommand struct {
	description string
	args        []commands.Argument
	run         func(ws Workspace, sessionID string, values map[string]string) (expansion, error)
}

func builtinCommands() []slashCommand {
	question := commands.Argument{ID: "QUESTION", Title: "question", Required: true}
	return []slashCommand{
		{name: "compact", builtin: &builtinCommand{
			description: "Summarize the conversation to free up context",
			run: func(ws Workspace, sessionID string, _ map[string]string) (expansion, error) {
				return expansion{act: func(ctx context.Context) (string, error) {
					if err := ws.AgentSummarize(ctx, sessionID); err != nil {
						return "", err
					}
					return "Session compacted.", nil
				}}, nil
			},
		}},
		{name: "title", builtin: &builtinCommand{
			description: "Generate the session title again",
			run: func(ws Workspace, sessionID string, _ map[string]string) (expansion, error) {
				return expansion{act: func(ctx context.Context) (string, error) {
					if err := ws.AgentRegenerateTitle(ctx, sessionID); err != nil {
						return "", err
					}
					return "Session title updated.", nil
				}}, nil
			},
		}},
		{name: "btw", builtin: &builtinCommand{
			description: "Ask a side question without adding it to the conversation",
			args:        []commands.Argument{question},
			run: func(ws Workspace, sessionID string, values map[string]string) (expansion, error) {
				return expansion{act: func(ctx context.Context) (string, error) {
					return ws.AgentAskSideQuestion(ctx, sessionID, values[question.ID])
				}}, nil
			},
		}},
		{name: "init", builtin: &builtinCommand{
			description: "Examine the project and write its context file",
			run: func(ws Workspace, _ string, _ map[string]string) (expansion, error) {
				prompt, err := ws.InitializePrompt()
				if err != nil {
					return expansion{}, internalError("/init: %v", err)
				}
				if err := ws.MarkProjectInitialized(); err != nil {
					slog.Warn("Failed to mark the project as initialized", "error", err)
				}
				return expansion{text: prompt}, nil
			},
		}},
		{name: "reload", builtin: &builtinCommand{
			description: "Reload config, skills, MCP servers and commands from disk",
			run: func(ws Workspace, _ string, _ map[string]string) (expansion, error) {
				return expansion{act: func(ctx context.Context) (string, error) {
					return ws.Reload(ctx)
				}}, nil
			},
		}},
	}
}

// builtinError words a failed built-in for the editor.
func builtinError(command string, err error) error {
	switch {
	case errors.Is(err, agent.ErrSessionBusy):
		return internalError("/%s: the agent is busy, wait for the current turn to finish", command)
	case errors.Is(err, agent.ErrNoTitlePrompt):
		return internalError("/%s: the session has no prompt to title yet", command)
	default:
		return internalError("/%s: %v", command, err)
	}
}
