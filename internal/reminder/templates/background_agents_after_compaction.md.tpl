The following background agents were launched before the conversation was summarized and are still running:
{{range .Agents}}- {{.}}
{{end}}
Each one reports back with a notification when it ends, and that notification is not something the user said. Do not launch them again, and do not poll for them with {{.StatusTool}}. Continue other work, or end your turn if their results are all you are waiting for. To cancel one, pass its agent_id to {{.StopTool}}.
