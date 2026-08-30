A powerful search tool built on ripgrep.

- ALWAYS use this tool for content search. NEVER invoke `grep` or `rg` as a bash command; this tool is optimized for correct permissions and access.
- Supports full regex syntax (e.g., "log.*Error", "function\s+\w+"), or literal text.
- Pattern syntax uses ripgrep, not grep: literal braces need escaping (use `interface\{\}` to find `interface{}` in Go code).
- Patterns match within single lines by default. For cross-line patterns like `struct \{[\s\S]*?field`, enable multiline matching.
- Returns matching file paths sorted by modification time (max {{ .MaxResults }}); respects .gitignore.
- Use glob to filter by filename, not contents. Use the agent tool for open-ended searches requiring multiple rounds.
