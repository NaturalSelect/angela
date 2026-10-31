package acp

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/agent/tools/mcp"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/skills"
)

func TestSplitCommand(t *testing.T) {
	t.Parallel()

	cases := []struct {
		text, name, input string
		ok                bool
	}{
		{"/user:review src/main.go", "user:review", "src/main.go", true},
		{"  /user:review   a  b  ", "user:review", "a  b", true},
		{"/user:review", "user:review", "", true},
		{"/user:review\nfirst\nsecond", "user:review", "first\nsecond", true},
		{"/", "", "", false},
		{"", "", "", false},
		{"review /user:review", "", "", false},
	}
	for _, tc := range cases {
		name, input, ok := splitCommand(tc.text)
		require.Equal(t, tc.ok, ok, tc.text)
		require.Equal(t, tc.name, name, tc.text)
		require.Equal(t, tc.input, input, tc.text)
	}
}

func TestBindArguments(t *testing.T) {
	t.Parallel()

	one := []commands.Argument{{ID: "FILE", Title: "File", Required: true}}
	two := []commands.Argument{{ID: "FILE", Required: true}, {ID: "MODE"}}

	t.Run("no arguments", func(t *testing.T) {
		t.Parallel()
		got, err := bindArguments(nil, "ignored")
		require.NoError(t, err)
		require.Empty(t, got)
	})

	t.Run("one argument takes the whole text", func(t *testing.T) {
		t.Parallel()
		got, err := bindArguments(one, "src/a b.go")
		require.NoError(t, err)
		require.Equal(t, map[string]string{"FILE": "src/a b.go"}, got)
	})

	t.Run("one required argument cannot be left out", func(t *testing.T) {
		t.Parallel()
		_, err := bindArguments(one, "")
		require.ErrorContains(t, err, "File is required")
	})

	t.Run("several arguments take name=value pairs", func(t *testing.T) {
		t.Parallel()
		got, err := bindArguments(two, `file="a b.go" MODE=fast`)
		require.NoError(t, err)
		require.Equal(t, map[string]string{"FILE": "a b.go", "MODE": "fast"}, got)
	})

	t.Run("an optional argument can be left out", func(t *testing.T) {
		t.Parallel()
		got, err := bindArguments(two, "FILE=a.go")
		require.NoError(t, err)
		require.Equal(t, map[string]string{"FILE": "a.go"}, got)
	})

	t.Run("a missing required argument is named", func(t *testing.T) {
		t.Parallel()
		_, err := bindArguments(two, "MODE=fast")
		require.ErrorContains(t, err, "FILE is required")
	})

	t.Run("an unknown argument is named", func(t *testing.T) {
		t.Parallel()
		_, err := bindArguments(two, "FILE=a.go COLOR=red")
		require.ErrorContains(t, err, "unknown argument COLOR")
	})

	t.Run("plain text is not name=value", func(t *testing.T) {
		t.Parallel()
		_, err := bindArguments(two, "a.go fast")
		require.ErrorContains(t, err, `expected NAME=value, got "a.go"`)
	})

	t.Run("an unclosed quote is reported", func(t *testing.T) {
		t.Parallel()
		_, err := bindArguments(two, `FILE="a.go`)
		require.ErrorContains(t, err, "missing closing quote for FILE")
	})
}

func TestSubstituteArguments(t *testing.T) {
	t.Parallel()

	t.Run("longest name wins", func(t *testing.T) {
		t.Parallel()
		got := substituteArguments("$FILENAME and $FILE", map[string]string{"FILE": "a", "FILENAME": "b"})
		require.Equal(t, "b and a", got)
	})

	t.Run("a value that looks like a placeholder is left alone", func(t *testing.T) {
		t.Parallel()
		got := substituteArguments("$A then $B", map[string]string{"A": "$B", "B": "x"})
		require.Equal(t, "$B then x", got)
	})

	t.Run("no values leaves the text alone", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "keep $THIS", substituteArguments("keep $THIS", nil))
	})
}

func skillEntry(name string) skills.CatalogEntry {
	return skills.CatalogEntry{
		ID:            "/skills/" + name + "/SKILL.md",
		Name:          name,
		Description:   "Does " + name,
		Label:         "user:" + name,
		UserInvocable: true,
	}
}

func reviewCommand() commands.CustomCommand {
	return commands.CustomCommand{
		ID:        "user:review",
		Name:      "user:review",
		Content:   "Review $FILE carefully",
		Arguments: []commands.Argument{{ID: "FILE", Title: "FILE", Required: true}},
	}
}

var builtinAdverts = availableCommands(builtinCommands())

// withBuiltins is what the editor is sent when the workspace offers extra
// on top of the built-in commands.
func withBuiltins(extra ...protocol.AvailableCommand) []protocol.AvailableCommand {
	return append(slices.Clone(builtinAdverts), extra...)
}

// beyondBuiltins drops the built-in commands from a loaded list.
func beyondBuiltins(cmds []slashCommand) []slashCommand {
	return cmds[len(builtinAdverts):]
}

func TestLoadCommands(t *testing.T) {
	t.Parallel()

	t.Run("gathers custom commands, skills and MCP prompts", func(t *testing.T) {
		t.Parallel()
		ws := newFakeWorkspace()
		ws.customCommands = []commands.CustomCommand{reviewCommand(), {ID: "project:notes", Name: "project:notes", Content: "Take notes"}}
		ws.skillEntries = []skills.CatalogEntry{skillEntry("jq"), {ID: "/skills/hidden/SKILL.md", Name: "hidden", Label: "user:hidden"}}
		ws.mcpPrompts = []commands.MCPPrompt{
			{ID: "srv:zeta", PromptID: "zeta", ClientID: "srv", Description: "Z"},
			{ID: "srv:alpha", PromptID: "alpha", ClientID: "srv", Title: "Alpha"},
		}

		got := availableCommands(loadCommands(t.Context(), ws))

		require.Equal(t, withBuiltins(
			protocol.AvailableCommand{Name: "user:review", Description: "Custom command", InputHint: "FILE"},
			protocol.AvailableCommand{Name: "project:notes", Description: "Custom command"},
			protocol.AvailableCommand{Name: "user:jq", Description: "Does jq", InputHint: "what to do with this skill (optional)"},
			protocol.AvailableCommand{Name: "srv:alpha", Description: "Alpha"},
			protocol.AvailableCommand{Name: "srv:zeta", Description: "Z"},
		), got)
	})

	t.Run("a built-in command cannot be replaced", func(t *testing.T) {
		t.Parallel()
		ws := newFakeWorkspace()
		ws.customCommands = []commands.CustomCommand{{ID: "compact", Name: "compact", Content: "mine"}}

		cmds := loadCommands(t.Context(), ws)

		require.Len(t, cmds, len(builtinAdverts))
		require.NotNil(t, cmds[0].builtin)
	})

	t.Run("the first command with a name wins", func(t *testing.T) {
		t.Parallel()
		ws := newFakeWorkspace()
		ws.customCommands = []commands.CustomCommand{reviewCommand()}
		ws.skillEntries = []skills.CatalogEntry{{ID: "/skills/review/SKILL.md", Name: "review", Label: "user:review", UserInvocable: true}}

		cmds := beyondBuiltins(loadCommands(t.Context(), ws))

		require.Len(t, cmds, 1)
		require.False(t, cmds[0].skill())
	})

	t.Run("a name the editor could not type is skipped", func(t *testing.T) {
		t.Parallel()
		ws := newFakeWorkspace()
		ws.mcpPrompts = []commands.MCPPrompt{{ID: "my server:greet"}, {ID: "srv:greet"}}

		cmds := beyondBuiltins(loadCommands(t.Context(), ws))

		require.Len(t, cmds, 1)
		require.Equal(t, "srv:greet", cmds[0].name)
	})

	t.Run("several arguments are hinted as name=value", func(t *testing.T) {
		t.Parallel()
		ws := newFakeWorkspace()
		ws.mcpPrompts = []commands.MCPPrompt{{ID: "srv:p", Arguments: []commands.Argument{{ID: "A"}, {ID: "B"}}}}

		got := availableCommands(beyondBuiltins(loadCommands(t.Context(), ws)))

		require.Equal(t, "A=value B=value", got[0].InputHint)
	})
}

func commandLists(c *fakeClient, sessionID string) [][]protocol.AvailableCommand {
	var out [][]protocol.AvailableCommand
	for _, u := range c.updates(sessionID) {
		if update, ok := u.(protocol.AvailableCommandsUpdate); ok {
			out = append(out, update.Commands)
		}
	}
	return out
}

var reviewAdvert = protocol.AvailableCommand{Name: "user:review", Description: "Custom command", InputHint: "FILE"}

func TestCommandsAreAdvertised(t *testing.T) {
	t.Parallel()

	t.Run("a new session gets them only after its response is written", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.customCommands = []commands.CustomCommand{reviewCommand()}

		resp, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd})
		require.NoError(t, err)
		require.NotNil(t, resp.AfterSent)
		require.Empty(t, commandLists(h.client, resp.SessionID))

		resp.AfterSent(t.Context())

		require.Equal(t, [][]protocol.AvailableCommand{withBuiltins(reviewAdvert)}, commandLists(h.client, resp.SessionID))
	})

	t.Run("a new session with nothing else still gets the built-ins", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)

		resp, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd})
		require.NoError(t, err)
		resp.AfterSent(t.Context())

		require.Equal(t, [][]protocol.AvailableCommand{withBuiltins()}, commandLists(h.client, resp.SessionID))
	})

	t.Run("loading and resuming a session send them", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.customCommands = []commands.CustomCommand{reviewCommand()}
		h.ws.addSession(session.Session{ID: "old", Title: "Old"})

		_, err := h.agent.LoadSession(t.Context(), protocol.LoadSessionRequest{SessionID: "old", Cwd: testCwd})
		require.NoError(t, err)
		require.Equal(t, [][]protocol.AvailableCommand{withBuiltins(reviewAdvert)}, commandLists(h.client, "old"))

		_, err = h.agent.ResumeSession(t.Context(), protocol.ResumeSessionRequest{SessionID: "old", Cwd: testCwd})
		require.NoError(t, err)
		require.Equal(t, [][]protocol.AvailableCommand{withBuiltins(reviewAdvert), withBuiltins(reviewAdvert)}, commandLists(h.client, "old"))
	})

	t.Run("an MCP change sends the new list, and only when it differs", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.customCommands = []commands.CustomCommand{reviewCommand()}
		resp, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd})
		require.NoError(t, err)
		resp.AfterSent(t.Context())

		h.ws.publish(pubsub.Event[mcp.Event]{Type: pubsub.UpdatedEvent, Payload: mcp.Event{Type: mcp.EventStateChanged, Name: "srv"}})
		h.ws.publish(pubsub.Event[mcp.Event]{Type: pubsub.UpdatedEvent, Payload: mcp.Event{Type: mcp.EventToolsListChanged, Name: "srv"}})
		h.ws.setMCPPrompts(commands.MCPPrompt{ID: "srv:greet", PromptID: "greet", ClientID: "srv", Description: "Greet"})
		h.ws.publish(pubsub.Event[mcp.Event]{Type: pubsub.UpdatedEvent, Payload: mcp.Event{Type: mcp.EventPromptsListChanged, Name: "srv"}})

		eventually(t, func() bool { return len(commandLists(h.client, resp.SessionID)) == 2 })
		require.Equal(t, [][]protocol.AvailableCommand{
			withBuiltins(reviewAdvert),
			withBuiltins(reviewAdvert, protocol.AvailableCommand{Name: "srv:greet", Description: "Greet"}),
		}, commandLists(h.client, resp.SessionID))
	})

	t.Run("a skills change sends the new list", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		resp, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd})
		require.NoError(t, err)
		resp.AfterSent(t.Context())

		h.ws.snapshot(func() { h.ws.customCommands = []commands.CustomCommand{reviewCommand()} })
		h.ws.publish(pubsub.Event[skills.Event]{Type: pubsub.UpdatedEvent, Payload: skills.Event{}})

		eventually(t, func() bool { return len(commandLists(h.client, resp.SessionID)) == 2 })
		require.Equal(t, [][]protocol.AvailableCommand{
			withBuiltins(),
			withBuiltins(reviewAdvert),
		}, commandLists(h.client, resp.SessionID))
	})

	t.Run("a server that goes away shortens the list", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.setMCPPrompts(commands.MCPPrompt{ID: "srv:greet", PromptID: "greet", ClientID: "srv", Description: "Greet"})
		resp, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd})
		require.NoError(t, err)
		resp.AfterSent(t.Context())

		h.ws.setMCPPrompts()
		h.ws.publish(pubsub.Event[mcp.Event]{Type: pubsub.UpdatedEvent, Payload: mcp.Event{Type: mcp.EventStateChanged, Name: "srv"}})

		eventually(t, func() bool { return len(commandLists(h.client, resp.SessionID)) == 2 })
		require.Equal(t, withBuiltins(), commandLists(h.client, resp.SessionID)[1])
	})

	t.Run("an MCP change before the response is written sends nothing yet", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		resp, err := h.agent.NewSession(t.Context(), protocol.NewSessionRequest{Cwd: testCwd})
		require.NoError(t, err)
		h.ws.setMCPPrompts(commands.MCPPrompt{ID: "srv:greet", PromptID: "greet", ClientID: "srv", Description: "Greet"})

		h.ws.publish(pubsub.Event[mcp.Event]{Type: pubsub.UpdatedEvent, Payload: mcp.Event{Type: mcp.EventStateChanged, Name: "srv"}})
		// The pump handles events in order, so once the title update below
		// reaches the editor, the MCP event before it has been handled too.
		h.ws.publish(pubsub.Event[session.Session]{Type: pubsub.UpdatedEvent, Payload: session.Session{ID: resp.SessionID, Title: "Marker"}})
		eventually(t, func() bool {
			for _, u := range h.client.updates(resp.SessionID) {
				if info, ok := u.(protocol.SessionInfoUpdate); ok && *info.Title == "Marker" {
					return true
				}
			}
			return false
		})
		require.Empty(t, commandLists(h.client, resp.SessionID))

		resp.AfterSent(t.Context())

		require.Equal(t, [][]protocol.AvailableCommand{withBuiltins(protocol.AvailableCommand{Name: "srv:greet", Description: "Greet"})}, commandLists(h.client, resp.SessionID))
	})
}

type promptCapture struct {
	text        string
	attachments []message.Attachment
}

// capturePrompt makes the next run record what it was asked to do.
func (h *harness) capturePrompt() *promptCapture {
	got := &promptCapture{}
	h.ws.run = func(ctx context.Context, sessionID, prompt string, atts []message.Attachment) error {
		got.text, got.attachments = prompt, atts
		h.finishRun(ctx, sessionID, notify.RunComplete{})
		return nil
	}
	return got
}

func (h *harness) ran() bool {
	var ran bool
	h.ws.snapshot(func() {
		for _, step := range h.ws.trace {
			ran = ran || step == "run"
		}
	})
	return ran
}

func TestPromptExpandsSlashCommands(t *testing.T) {
	t.Parallel()

	t.Run("a custom command fills in its argument", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.customCommands = []commands.CustomCommand{reviewCommand()}
		sessionID := h.newSession(t)
		got := h.capturePrompt()

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/user:review src/main.go"))

		require.NoError(t, err)
		require.Equal(t, "Review src/main.go carefully", got.text)
		require.Empty(t, got.attachments)
	})

	t.Run("a custom command without arguments keeps the text after it", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.customCommands = []commands.CustomCommand{{ID: "project:notes", Name: "project:notes", Content: "Take notes"}}
		sessionID := h.newSession(t)
		got := h.capturePrompt()

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/project:notes about the parser"))

		require.NoError(t, err)
		require.Equal(t, "Take notes\n\nabout the parser", got.text)
	})

	t.Run("a skill is attached next to what the user typed", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		entry := skillEntry("jq")
		h.ws.skillEntries = []skills.CatalogEntry{entry}
		h.ws.skillBodies = map[string][]byte{entry.ID: []byte("# jq\nUse jq.")}
		sessionID := h.newSession(t)
		got := h.capturePrompt()

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/user:jq extract the names"))

		require.NoError(t, err)
		require.Equal(t, "extract the names", got.text)
		require.Equal(t, []message.Attachment{{
			FilePath: "jq", FileName: "jq", MimeType: "text/markdown", Content: []byte("# jq\nUse jq."),
		}}, got.attachments)
	})

	t.Run("a skill with no text after it still runs", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		entry := skillEntry("jq")
		h.ws.skillEntries = []skills.CatalogEntry{entry}
		h.ws.skillBodies = map[string][]byte{entry.ID: []byte("# jq")}
		sessionID := h.newSession(t)
		got := h.capturePrompt()

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/user:jq"))

		require.NoError(t, err)
		require.Empty(t, got.text)
		require.Len(t, got.attachments, 1)
	})

	t.Run("an MCP prompt is fetched from its server", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.mcpPrompts = []commands.MCPPrompt{{
			ID: "srv:greet", PromptID: "greet", ClientID: "srv",
			Arguments: []commands.Argument{{ID: "NAME", Title: "NAME", Required: true}},
		}}
		var asked struct {
			client, prompt string
			args           map[string]string
		}
		h.ws.promptText = func(clientID, promptID string, args map[string]string) (string, error) {
			asked.client, asked.prompt, asked.args = clientID, promptID, args
			return "Say hello to " + args["NAME"], nil
		}
		sessionID := h.newSession(t)
		got := h.capturePrompt()

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/srv:greet Ada"))

		require.NoError(t, err)
		require.Equal(t, "Say hello to Ada", got.text)
		require.Equal(t, "srv", asked.client)
		require.Equal(t, "greet", asked.prompt)
		require.Equal(t, map[string]string{"NAME": "Ada"}, asked.args)
	})

	t.Run("an unknown command goes to the model unchanged", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.customCommands = []commands.CustomCommand{reviewCommand()}
		sessionID := h.newSession(t)
		got := h.capturePrompt()

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/etc/hosts has a bad entry"))

		require.NoError(t, err)
		require.Equal(t, "/etc/hosts has a bad entry", got.text)
	})

	t.Run("a command only counts at the start of the prompt", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.customCommands = []commands.CustomCommand{reviewCommand()}
		sessionID := h.newSession(t)
		got := h.capturePrompt()

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "please run /user:review a.go"))

		require.NoError(t, err)
		require.Equal(t, "please run /user:review a.go", got.text)
	})

	t.Run("bad arguments are refused without starting a run", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.customCommands = []commands.CustomCommand{reviewCommand()}
		sessionID := h.newSession(t)
		h.capturePrompt()

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/user:review"))

		requireCode(t, err, protocol.CodeInvalidParams)
		require.ErrorContains(t, err, "Usage: /user:review FILE")
		require.False(t, h.ran())
	})

	t.Run("an MCP prompt that fails is reported without starting a run", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.mcpPrompts = []commands.MCPPrompt{{ID: "srv:greet", PromptID: "greet", ClientID: "srv"}}
		sessionID := h.newSession(t)
		h.capturePrompt()

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/srv:greet"))

		requireCode(t, err, protocol.CodeInternal)
		require.ErrorContains(t, err, "run /srv:greet")
		require.False(t, h.ran())
	})

	t.Run("a skill that cannot be read is reported without starting a run", func(t *testing.T) {
		t.Parallel()
		h := newHarness(t)
		h.ws.skillEntries = []skills.CatalogEntry{skillEntry("jq")}
		sessionID := h.newSession(t)
		h.capturePrompt()

		_, err := h.agent.Prompt(t.Context(), textPrompt(sessionID, "/user:jq"))

		requireCode(t, err, protocol.CodeInternal)
		require.ErrorContains(t, err, "read skill /user:jq")
		require.False(t, h.ran())
	})
}
