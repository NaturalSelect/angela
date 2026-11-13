Get Angela's current runtime state: active model, provider, LSP/MCP status, skills, hooks, permissions, sandbox, and disabled tools. No parameters needed.

<usage>
- Shows active model and provider, LSP/MCP server status, skills,
  hooks, permissions mode, sandbox restrictions, disabled tools, and key
  options
- Use when diagnosing why something isn't working (missing diagnostics,
  provider errors, MCP disconnections)
- No parameters needed — always returns the full current state
</usage>

<tips>
- Check [lsp] and [mcp] sections for service health
- Check [providers] to see which providers are enabled and available
- Check [sandbox] to see whether the process is confined and which paths are
  writable. Look here when a write or a network call fails with a permission
  error even though the permission system approved it
- Check [skills] to see which skills are available and whether they have been
  loaded this session
- Check [hooks] to see which hook events are configured and whether the
  hook runner is active
- Pair with the angela-config skill to fix configuration issues
</tips>
