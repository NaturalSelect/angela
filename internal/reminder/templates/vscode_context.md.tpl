{{.Heading}}
Active file: {{.ActiveFile}}
{{- if .Selection}}
{{if eq .Selection.StartLine .Selection.EndLine}}Selected line {{.Selection.StartLine}}{{else}}Selected lines {{.Selection.StartLine}}-{{.Selection.EndLine}}{{end}}:
{{.Selection.Fence}}
{{.Selection.Text}}
{{.Selection.Fence}}
{{- if .Selection.Truncated}}
Only the first part of the selection is shown. Read the file for the rest.
{{- end}}
{{- end}}
{{- if .RecentFiles}}
Other files the user looked at recently, newest first:
{{- range .RecentFiles}}
- {{.}}
{{- end}}
{{- end}}

This is what happens to be on the user's screen, not part of their request. Use it when it helps, for example to resolve "this function" or "the selected code", and ignore it otherwise.
