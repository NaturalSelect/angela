Reload Angela's configuration, skills and MCP servers from disk; use after editing angela.json, adding or changing a skill, or changing an MCP server entry.

<usage>
- Re-reads every angela.json layer, rediscovers skills, refreshes the sub-agent table and starts, stops or restarts MCP servers to match the new config
- Hook commands in the new config run before tool calls from the next turn on
- The user is asked to approve every call, with no "Allow for Session" option, because reloading can start MCP server processes and activate hook commands named in the config; only yolo mode skips the prompt
- If the config fails to parse, nothing changes and the parse error is returned
</usage>

<limits>
- The tool list and system prompt of the current turn are already fixed, so new skills and MCP tools appear from the next turn
- Permission rules, LSP server definitions and TUI startup options are not reloaded; use LSPRestart for LSP servers
</limits>
