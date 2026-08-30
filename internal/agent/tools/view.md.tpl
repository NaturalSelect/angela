Reads a file from the local filesystem by absolute path, prefixing each line with its number (right-aligned, then `|`).

- The path must be absolute, not relative. It is okay to read a file that does not exist; an error will be returned.
- Reads up to {{ .DefaultReadLimit }} lines from the start by default; use offset and limit to read later sections. At most {{ .MaxViewSizeKB }}KB of file content is returned.
- Renders images (PNG, JPEG, GIF, WebP) visually.
- Reads files only, not directories — use ls for those.
- If you read a file that exists but has empty contents you will receive a warning in place of file contents.
