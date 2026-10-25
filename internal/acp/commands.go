package acp

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
	"github.com/NaturalSelect/angela/internal/commands"
	"github.com/NaturalSelect/angela/internal/message"
)

// slashCommand is one entry of the editor's "/" menu. Exactly one of custom
// and prompt is set. A custom command that carries a skill is a skill.
type slashCommand struct {
	name   string
	custom *commands.CustomCommand
	prompt *commands.MCPPrompt
}

func (c slashCommand) skill() bool {
	return c.custom != nil && c.custom.Skill != nil
}

func (c slashCommand) arguments() []commands.Argument {
	if c.prompt != nil {
		return c.prompt.Arguments
	}
	return c.custom.Arguments
}

func (c slashCommand) description() string {
	switch {
	case c.prompt != nil:
		return cmp.Or(c.prompt.Description, c.prompt.Title, "Prompt from MCP server "+c.prompt.ClientID)
	case c.skill():
		return cmp.Or(c.custom.Skill.Description, "Skill")
	default:
		return "Custom command"
	}
}

// inputHint is what the editor shows before the user has typed anything
// after the command name. It is empty when the command takes no input.
func (c slashCommand) inputHint() string {
	args := c.arguments()
	switch {
	case c.skill():
		return "what to do with this skill (optional)"
	case len(args) == 0:
		return ""
	case len(args) == 1:
		return cmp.Or(args[0].Title, args[0].ID)
	default:
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = a.ID + "=value"
		}
		return strings.Join(parts, " ")
	}
}

// loadCommands gathers everything the TUI's command palette offers: custom
// command files, user-invocable skills and MCP prompts. The first command
// with a given name wins, and names the editor could not type are skipped.
//
// NOTE: MCP prompts come out of a map, so they are sorted. Otherwise every
// load would look like a change and the list would be sent again.
func loadCommands(ctx context.Context, ws Workspace) []slashCommand {
	var out []slashCommand
	seen := make(map[string]struct{})
	add := func(c slashCommand) {
		if c.name == "" || strings.ContainsFunc(c.name, unicode.IsSpace) {
			return
		}
		if _, dup := seen[c.name]; dup {
			return
		}
		seen[c.name] = struct{}{}
		out = append(out, c)
	}

	custom, err := ws.ListCustomCommands(ctx)
	if err != nil {
		slog.Warn("Failed to load custom commands", "error", err)
	}
	entries, err := ws.ListSkills(ctx)
	if err != nil {
		slog.Warn("Failed to load skill commands", "error", err)
	}
	custom = append(custom, commands.FromSkillCatalog(entries)...)
	for i := range custom {
		add(slashCommand{name: custom[i].Name, custom: &custom[i]})
	}

	prompts, err := ws.ListMCPPrompts(ctx)
	if err != nil {
		slog.Warn("Failed to load MCP prompts", "error", err)
	}
	slices.SortFunc(prompts, func(a, b commands.MCPPrompt) int { return cmp.Compare(a.ID, b.ID) })
	for i := range prompts {
		add(slashCommand{name: prompts[i].ID, prompt: &prompts[i]})
	}
	return out
}

func availableCommands(cmds []slashCommand) []protocol.AvailableCommand {
	out := make([]protocol.AvailableCommand, len(cmds))
	for i, c := range cmds {
		out[i] = protocol.AvailableCommand{Name: c.name, Description: c.description(), InputHint: c.inputHint()}
	}
	return out
}

type commandLoader func() []protocol.AvailableCommand

func commandsOf(ctx context.Context, ws Workspace) commandLoader {
	return func() []protocol.AvailableCommand { return availableCommands(loadCommands(ctx, ws)) }
}

// openCommands makes a session eligible for command updates and sends it
// its first list.
//
// NOTE: A session stays closed until the editor can route updates for it.
// For session/new that is after the response is written, so an MCP event
// that lands earlier must not send, or the update would be dropped and
// remembered as delivered. The list is read under the lock, so a change
// between reading and sending cannot leave an older list behind.
func openCommands(ctx context.Context, c protocol.Client, s *liveSession, load commandLoader) {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	s.cmdOpen = true
	sendCommands(ctx, c, s, load())
}

// refreshCommands sends the list again if it changed since the last one.
func refreshCommands(ctx context.Context, c protocol.Client, s *liveSession, load commandLoader) {
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	if s.cmdOpen {
		sendCommands(ctx, c, s, load())
	}
}

// sendCommands needs s.cmdMu. An empty list is only sent to replace a
// longer one.
func sendCommands(ctx context.Context, c protocol.Client, s *liveSession, list []protocol.AvailableCommand) {
	if slices.Equal(s.commands, list) {
		return
	}
	err := c.SessionUpdate(ctx, protocol.SessionNotification{
		SessionID: s.id,
		Update:    protocol.AvailableCommandsUpdate{Commands: list},
	})
	if err != nil {
		slog.Warn("Failed to send available commands", "session_id", s.id, "error", err)
		return
	}
	s.commands = list
}

// expandCommand turns a prompt that starts with a known /command into what
// the TUI would have sent for it, plus any files the command attaches.
// Anything else is returned unchanged, because a leading slash is as likely
// to be a path.
func expandCommand(ctx context.Context, ws Workspace, text string) (string, []message.Attachment, error) {
	name, input, ok := splitCommand(text)
	if !ok {
		return text, nil, nil
	}
	cmds := loadCommands(ctx, ws)
	i := slices.IndexFunc(cmds, func(c slashCommand) bool { return c.name == name })
	if i < 0 {
		return text, nil, nil
	}
	return cmds[i].expand(ctx, ws, input)
}

func splitCommand(text string) (name, input string, ok bool) {
	body, ok := strings.CutPrefix(strings.TrimSpace(text), "/")
	if !ok || body == "" {
		return "", "", false
	}
	name, rest, _ := cutSpace(body)
	return name, strings.TrimSpace(rest), true
}

// expand mirrors the TUI. A custom command has its placeholders filled in,
// a skill is attached as a markdown file next to what the user typed, and
// an MCP prompt is fetched from its server.
func (c slashCommand) expand(ctx context.Context, ws Workspace, input string) (string, []message.Attachment, error) {
	switch {
	case c.skill():
		content, res, err := ws.ReadSkill(ctx, c.custom.Skill.SkillFilePath)
		if err != nil {
			return "", nil, internalError("read skill /%s: %v", c.name, err)
		}
		name := cmp.Or(res.Name, c.custom.Skill.Name)
		skill := message.Attachment{FilePath: name, FileName: name, MimeType: "text/markdown", Content: content}
		return input, []message.Attachment{skill}, nil

	case c.prompt != nil:
		values, err := bindArguments(c.arguments(), input)
		if err != nil {
			return "", nil, c.usageError(err)
		}
		text, err := ws.GetMCPPrompt(c.prompt.ClientID, c.prompt.PromptID, values)
		if err != nil {
			return "", nil, internalError("run /%s: %v", c.name, err)
		}
		if strings.TrimSpace(text) == "" {
			return "", nil, internalError("/%s returned no text", c.name)
		}
		return text, nil, nil

	default:
		values, err := bindArguments(c.arguments(), input)
		if err != nil {
			return "", nil, c.usageError(err)
		}
		text := substituteArguments(c.custom.Content, values)
		if len(c.arguments()) == 0 && input != "" {
			text += "\n\n" + input
		}
		return text, nil, nil
	}
}

func (c slashCommand) usageError(err error) error {
	return invalidParams("/%s: %v. Usage: /%s %s", c.name, err, c.name, c.inputHint())
}

// bindArguments maps the text after a command name onto its arguments. One
// argument takes the whole text. Several take NAME=value pairs, where a
// value with spaces is wrapped in double quotes.
func bindArguments(args []commands.Argument, input string) (map[string]string, error) {
	switch len(args) {
	case 0:
		return nil, nil
	case 1:
		if input != "" {
			return map[string]string{args[0].ID: input}, nil
		}
		if args[0].Required {
			return nil, fmt.Errorf("%s is required", cmp.Or(args[0].Title, args[0].ID))
		}
		return nil, nil
	}

	pairs, err := parseKeyValues(input)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string, len(pairs))
	for key, value := range pairs {
		i := slices.IndexFunc(args, func(a commands.Argument) bool { return strings.EqualFold(a.ID, key) })
		if i < 0 {
			return nil, fmt.Errorf("unknown argument %s", key)
		}
		values[args[i].ID] = value
	}
	for _, a := range args {
		if _, ok := values[a.ID]; !ok && a.Required {
			return nil, fmt.Errorf("%s is required", a.ID)
		}
	}
	return values, nil
}

func parseKeyValues(input string) (map[string]string, error) {
	out := make(map[string]string)
	for rest := strings.TrimSpace(input); rest != ""; rest = strings.TrimSpace(rest) {
		key, value, ok := strings.Cut(rest, "=")
		if !ok || key == "" || strings.ContainsFunc(key, unicode.IsSpace) {
			field, _, _ := cutSpace(rest)
			return nil, fmt.Errorf("expected NAME=value, got %q", field)
		}
		if quoted, ok := strings.CutPrefix(value, `"`); ok {
			text, after, closed := strings.Cut(quoted, `"`)
			if !closed {
				return nil, fmt.Errorf("missing closing quote for %s", key)
			}
			out[key], rest = text, after
			continue
		}
		out[key], rest, _ = cutSpace(value)
	}
	return out, nil
}

func cutSpace(s string) (before, after string, found bool) {
	if i := strings.IndexFunc(s, unicode.IsSpace); i >= 0 {
		return s[:i], s[i:], true
	}
	return s, "", false
}

// substituteArguments fills in $NAME placeholders in one pass, longest name
// first, so $FILE cannot eat the start of $FILENAME and a value that
// contains another placeholder is left alone.
func substituteArguments(content string, values map[string]string) string {
	if len(values) == 0 {
		return content
	}
	names := slices.SortedFunc(maps.Keys(values), func(a, b string) int {
		return cmp.Or(cmp.Compare(len(b), len(a)), cmp.Compare(a, b))
	})
	pairs := make([]string, 0, 2*len(names))
	for _, name := range names {
		pairs = append(pairs, "$"+name, values[name])
	}
	return strings.NewReplacer(pairs...).Replace(content)
}
