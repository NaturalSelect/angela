Writes a file to the local filesystem, overwriting if one exists; auto-creates parent dirs. Cannot append.

- View the file first when it already exists, or the call will fail.
- Prefer edit or multiedit for modifying existing files — they only send the diff. Only use this tool to create new files or for complete rewrites.
- NEVER create documentation files (*.md) or README files unless explicitly requested by the user.
- Only use emojis if the user explicitly requests it. Avoid writing emojis to files unless asked.
