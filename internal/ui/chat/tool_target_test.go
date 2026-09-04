package chat

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/stretchr/testify/require"
)

func TestToolCallTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		maxWidth int
		want     string
	}{
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "invalid json",
			input: "not json",
			want:  "",
		},
		{
			name:  "file_path uses base name",
			input: `{"file_path":"/home/user/project/main.go"}`,
			want:  "main.go",
		},
		{
			name:  "path uses base name",
			input: `{"path":"/home/user/project"}`,
			want:  "project",
		},
		{
			name:  "command is used verbatim",
			input: `{"command":"go test ./..."}`,
			want:  "go test ./...",
		},
		{
			name:  "pattern key",
			input: `{"pattern":"TODO"}`,
			want:  "TODO",
		},
		{
			name:  "query key",
			input: `{"query":"how does X work"}`,
			want:  "how does X work",
		},
		{
			name:  "url key",
			input: `{"url":"https://example.com"}`,
			want:  "https://example.com",
		},
		{
			name:  "description key",
			input: `{"description":"Run the build"}`,
			want:  "Run the build",
		},
		{
			name:  "priority: file_path wins over command",
			input: `{"command":"ls","file_path":"/a/b.go"}`,
			want:  "b.go",
		},
		{
			name:  "no recognizable target key",
			input: `{"unrelated":"value"}`,
			want:  "",
		},
		{
			name:  "empty value is skipped in favor of the next key",
			input: `{"file_path":"","command":"ls -la"}`,
			want:  "ls -la",
		},
		{
			name:     "truncates to maxWidth",
			input:    `{"command":"a very long command that will not fit"}`,
			maxWidth: 10,
			want:     "a very lo…",
		},
		{
			name:  "tabs expand to four spaces",
			input: `{"command":"go\ttest"}`,
			want:  "go    test",
		},
		{
			name:  "leading and trailing whitespace is trimmed",
			input: `{"command":"  go test  "}`,
			want:  "go test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tc := message.ToolCall{Input: tt.input}
			got := ToolCallTarget(tc, tt.maxWidth)
			require.Equal(t, tt.want, got)
		})
	}
}
