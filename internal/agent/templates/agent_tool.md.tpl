Launch a new agent to handle complex, multi-step tasks. Each agent type has specific capabilities and tools available to it.

When using the Agent tool, specify a subagent_type parameter to select which agent type to use.

## When to use

Reach for this when the task matches an available agent type, when you have independent work to run in parallel, or when answering would mean reading across several files — delegate it and you keep the conclusion, not the file dumps. For a single-fact lookup where you already know the file, symbol, or value, search directly. Once you've delegated a search, don't also run it yourself — wait for the result.

When NOT to use the Agent tool:
- If you want to read a specific file path, use the View or Glob tool instead, to find the match more quickly
- If you are searching for a specific definition like "func Foo", use the Grep tool instead, to find the match more quickly
- If you are searching for code within a specific file or set of 2-3 files, use the View tool instead, to find the match more quickly
- If no available agent is a good fit for the task, use other tools directly

## Usage notes

- Always include a short description summarizing what the agent will do.
- The agent's final message is returned to you as the tool result; it is not shown to the user — relay what matters in a concise summary.
- Trust but verify: an agent's summary describes what it intended to do, not necessarily what it did. When an agent writes or edits code, check the actual changes before reporting the work as done.
- Each agent invocation starts with a fresh context and has no memory of prior runs, so the prompt must be self-contained and highly detailed.
- Clearly tell the agent whether you expect it to write code or just to do research (search, file reads, web fetches), since a fresh agent is not aware of the user's intent.
- If an agent's description says it should be used proactively, try your best to use it without the user having to ask for it first.
- Each agent type's model and tool access come from its definition; you cannot override them per call.
- If the user asks you to run agents "in parallel", send a single message with multiple Agent tool use blocks.
- Once you have delegated work to an agent, do not duplicate that work yourself while it runs.

Available agent types:
{{- range .Agents}}
{{- if not .Branch}}
- {{.ID}}: {{.Description}}
{{- end}}
{{- end}}
{{- if .HasBranch}}

Branch agents:

These do not work on their own. Calling one forks the current conversation
into a side session, suspends this call, and hands control to the user, who
talks to it directly. Your call returns only when they finish — with their
summary if they merged the branch back, or with a note that they abandoned
it. Neither outcome is a failure.

Use one only when the work genuinely needs the user in the loop: a decision
you cannot make for them, an exploration whose direction only they can set,
or a discussion that has to happen before the task is even well-defined. For
anything you can carry out yourself, use an ordinary agent instead — a
branch stops all your progress until the user comes back to it.

You may fork several at once, and that is the point when the user needs to
compare: one call per direction lets them weigh alternatives on the same
question side by side, or carry several unrelated questions at the same
time. Each branch is its own conversation and comes back with its own
outcome. Your turn resumes only once every branch you forked is resolved, so
fork per direction the user would genuinely want to hold apart, not per
thought.
{{- range .Agents}}
{{- if .Branch}}
- {{.ID}}: {{.Description}}
{{- end}}
{{- end}}
{{- end}}
