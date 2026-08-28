Launch a new agent to handle complex, multistep tasks autonomously.

When using the agent tool, you must specify a subagent_type parameter to select which agent type to use.

When NOT to use the agent tool:
- If you want to read a specific file path, use the View or Glob tool instead, to find the match more quickly
- If you are searching for a specific definition like "func Foo", use the Grep tool instead, to find the match more quickly
- If you are searching for code within a specific file or set of 2-3 files, use the View tool instead, to find the match more quickly
- If no available agent is a good fit for the task, use other tools directly

Usage notes:
1. Launch multiple agents concurrently whenever possible, to maximize performance
2. Once you have delegated work to an agent, do not duplicate that work yourself
3. When the agent is done, it will return a single message back to you. The result is not visible to the user — summarize it for them
4. Each agent invocation starts with a fresh context. Your prompt should contain a highly detailed task description
5. The agent's outputs should generally be trusted
6. Clearly tell the agent whether you expect it to write code or just do research

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
{{- range .Agents}}
{{- if .Branch}}
- {{.ID}}: {{.Description}}
{{- end}}
{{- end}}
{{- end}}
