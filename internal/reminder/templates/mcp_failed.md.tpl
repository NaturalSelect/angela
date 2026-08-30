The following MCP servers are configured but failed to connect, so their tools are unavailable for this session:
{{range .}}- {{.}}
{{end}}
Treat this as a connection failure, not a missing capability: do not conclude the integration does not exist or was never configured. If the user's request depends on one of these servers, tell them it failed to connect so they can fix or retry it.
