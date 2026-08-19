Fetch raw content from a URL as text, markdown, or html (max {{ .MaxFetchSizeKB }}KB); no AI processing. For analysis, extraction, or summarization, dispatch the agent tool with subagent_type "web-fetch" instead.
{{- if .GhAvailable }} For GitHub content when an exact repo, issue, or PR link is provided, use `gh` CLI in bash instead.{{- end }}
