Performs exact string replacement in a file; can also create or delete content.

- You must view the file in this conversation before editing it, or the call will fail.
- `old_string` must match the file exactly, including indentation, and be unique — the edit fails otherwise. Strip the view line prefix (right-aligned line number then `|`, e.g. `    12|`) before matching.
- Keep `old_string` minimal — usually 1-3 lines, only enough to be unique in the file. Including excess context wastes tokens and is an error. If it is not unique, add the minimum extra context needed, or use `replace_all` to change every instance.
- `replace_all: true` replaces every occurrence instead; useful for renaming a variable across the file.
- If old_string differs from the file only in whitespace, the matching lines are still edited and new_string is re-indented to the file's style; the response says when this happened, so verify the result.
- For whole-function/method/type replacements prefer `lsp_replace_symbol` (no whitespace matching needed). For renames prefer `lsp_rename` (semantic, cross-file). For large edits use write.
- Only use emojis if the user explicitly requests it. Avoid adding emojis to files unless asked.
