package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAttribution_CommitTrailer pins the exact spacing of the
// attribution block appended to a commit message body: one blank
// line before each present paragraph, and no output at all when
// neither is configured. This must stay in lockstep with the
// equivalent HEREDOC the bash tool instructs the model to type by
// hand (see internal/agent/tools/bash.md.tpl's <git_commits>
// section), so a commit made through a non-chat path (e.g. the quick
// "commit" command) matches the format of one made in chat.
func TestAttribution_CommitTrailer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		attribution Attribution
		modelName   string
		email       string
		want        string
	}{
		{
			name:        "generated_with and assisted-by",
			attribution: Attribution{GeneratedWith: true, TrailerStyle: TrailerStyleAssistedBy},
			modelName:   "Claude Sonnet 5",
			want:        "\n\nGenerated with Angela\n\nAssisted-by: Angela:Claude Sonnet 5",
		},
		{
			name:        "generated_with and co-authored-by",
			attribution: Attribution{GeneratedWith: true, TrailerStyle: TrailerStyleCoAuthoredBy},
			modelName:   "Claude Sonnet 5",
			want:        "\n\nGenerated with Angela\n\nCo-Authored-By: Angela",
		},
		{
			name:        "co-authored-by with email",
			attribution: Attribution{TrailerStyle: TrailerStyleCoAuthoredBy},
			email:       "angela@example.com",
			want:        "\n\nCo-Authored-By: Angela <angela@example.com>",
		},
		{
			name:        "generated_with only",
			attribution: Attribution{GeneratedWith: true, TrailerStyle: TrailerStyleNone},
			modelName:   "Claude Sonnet 5",
			want:        "\n\nGenerated with Angela",
		},
		{
			name:        "assisted-by only",
			attribution: Attribution{GeneratedWith: false, TrailerStyle: TrailerStyleAssistedBy},
			modelName:   "Claude Sonnet 5",
			want:        "\n\nAssisted-by: Angela:Claude Sonnet 5",
		},
		{
			name:        "neither configured",
			attribution: Attribution{GeneratedWith: false, TrailerStyle: TrailerStyleNone},
			modelName:   "Claude Sonnet 5",
			want:        "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, tt.attribution.CommitTrailer(tt.modelName, tt.email))
		})
	}
}
