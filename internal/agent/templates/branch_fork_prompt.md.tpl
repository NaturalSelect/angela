You have been forked from {{if .ParentTitle}}"{{.ParentTitle}}"{{else}}the conversation above{{end}} as a branch.

Everything above this message is that conversation's history. It was the other
agent's context and it is now yours — read it as your own memory of how the
work got here, not as a transcript of someone else.

The last step in that history is the one that forked you. Its tool results
stayed with the conversation that made the calls, so they show here only as
placeholders.

The task below was written by the agent you were forked from, not typed by the
user. The user talks to you from the next message on.

Your task:

{{.Prompt}}

Work with the user on this, then call `Merge` to hand your proposal back to
the conversation you were forked from.
