package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStripBashDisplayPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cmd        string
		workingDir string
		want       string
	}{
		{
			name:       "matching cd prefix is stripped",
			cmd:        "cd /home/user/project && ls -la",
			workingDir: "/home/user/project",
			want:       "ls -la",
		},
		{
			name:       "a different directory is left untouched",
			cmd:        "cd /tmp && ls -la",
			workingDir: "/home/user/project",
			want:       "cd /tmp && ls -la",
		},
		{
			name:       "no cd prefix is left untouched",
			cmd:        "ls -la",
			workingDir: "/home/user/project",
			want:       "ls -la",
		},
		{
			name:       "empty command stays empty",
			cmd:        "",
			workingDir: "/home/user/project",
			want:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, StripBashDisplayPrefix(tt.cmd, tt.workingDir))
		})
	}
}
