Launch a new agent to handle complex, multi-step tasks. Each agent type has specific capabilities and tools available to it.

When using the Agent tool, specify a subagent_type parameter to select which agent type to use.

## When to use

Reach for this when the task matches an available agent type, when you have independent work to run in parallel, or when answering would mean reading across several files — delegate it and you keep the conclusion, not the file dumps. For a single-fact lookup where you already know the file, symbol, or value, search directly. Once you've delegated a search, don't also run it yourself — wait for the result.

When NOT to use the Agent tool:
- If you want to read a specific file path, use the Read or Glob tool instead, to find the match more quickly
- If you are searching for a specific definition like "func Foo", use the Grep tool instead, to find the match more quickly
- If you are searching for code within a specific file or set of 2-3 files, use the Read tool instead, to find the match more quickly
- If no available agent is a good fit for the task, use other tools directly

## Usage notes

- Always include a short description summarizing what the agent will do.
{{- if .Background}}
- A foreground agent's final message is returned to you as the tool result, and a background agent's arrives later in a notification. Neither is shown to the user — relay what matters in a concise summary.
{{- else}}
- The agent's final message is returned to you as the tool result; it is not shown to the user — relay what matters in a concise summary.
{{- end}}
- Trust but verify: an agent's summary describes what it intended to do, not necessarily what it did. When an agent writes or edits code, check the actual changes before reporting the work as done.
- Each ordinary agent invocation starts with a fresh context and has no memory of prior runs, so the prompt must be self-contained and highly detailed.
{{- if .HasBranch}}
- A branch agent is the exception: it forks with this whole conversation already behind it, so keep its prompt short — state the decision or question, not background it already has.
{{- end}}
- Clearly tell the agent whether you expect it to write code or just to do research (search, file reads, web fetches), since a fresh agent is not aware of the user's intent.
- If an agent's description says it should be used proactively, try your best to use it without the user having to ask for it first.
- Each agent type's model and tool access come from its definition; you cannot override them per call.
- If the user asks you to run agents "in parallel", send a single message with multiple Agent tool use blocks.
- Once you have delegated work to an agent, do not duplicate that work yourself while it runs.
{{- if .Background}}
- **Background agents**: an agent runs in the background only if you pass `run_in_background: true`. The call then returns at once with a receipt instead of the agent's report, and you will be automatically notified when the agent ends — do NOT sleep, poll, or proactively check on its progress. Continue with other work or respond to the user instead. If nothing else is left to do, tell the user what is running and end your turn: the notification starts a new turn.
- **Foreground vs background**: leave `run_in_background` off when your very next action depends on the agent's result and nothing else could usefully happen while it runs — e.g. a research agent whose finding gates the edit you are about to make. Use it for fire-and-forget work, independent investigations, and anything where the user might hand you something else in the meantime. Wanting the result "next" is not enough on its own.
- **Don't race**: after launching a background agent, you know nothing about its results. Never fabricate or predict them in any format — not as prose, summary, or structured output. The completion notification arrives in a later turn as a system reminder; it is never something you write yourself, and it is not the user speaking. If the user asks before it lands, say the agent is still running — give status, not a guess.
- Use {{.AgentStatusTool}} only to see which background agents are still running, never to wait for one. Use {{.AgentStopTool}} to end an agent the user no longer needs or whose work has been overtaken.
{{- if .HasBranch}}
- A branch agent cannot run in the background, and the agents you dispatch cannot launch background agents of their own.
{{- else}}
- The agents you dispatch cannot launch background agents of their own.
{{- end}}
{{- end}}
{{- if .HasSubagent}}

Available agent types:
{{- range .Agents}}
{{- if not .Branch}}
- {{.ID}}: {{.Description}}
{{- end}}
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
