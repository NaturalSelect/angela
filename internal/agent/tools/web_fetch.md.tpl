Fetch a web URL and return content as markdown; for use inside sub-agents. Large pages (>50KB) are saved to a temp file for grep/read.
{{- if .GhAvailable }} For GitHub content when an exact repo, issue, or PR link is provided, use `gh` CLI in bash instead.{{- end }}
