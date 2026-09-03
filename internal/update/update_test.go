package update

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestCheckForUpdate_Old(t *testing.T) {
	info, err := Check(t.Context(), "v0.10.0", newClient(t, "v0.11.0"))
	require.NoError(t, err)
	require.NotNil(t, info)
	require.True(t, info.Available())
}

func TestCheckForUpdate_Beta(t *testing.T) {
	t.Run("current is stable", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.10.0", newClient(t, "v0.11.0-beta.1"))
		require.NoError(t, err)
		require.NotNil(t, info)
		require.False(t, info.Available())
	})

	t.Run("current is also beta", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.11.0-beta.1", newClient(t, "v0.11.0-beta.2"))
		require.NoError(t, err)
		require.NotNil(t, info)
		require.True(t, info.Available())
	})

	t.Run("current is beta, latest isn't", func(t *testing.T) {
		info, err := Check(t.Context(), "v0.11.0-beta.1", newClient(t, "v0.11.0"))
		require.NoError(t, err)
		require.NotNil(t, info)
		require.True(t, info.Available())
	})
}

// newClient returns a MockClient whose Latest reports tag, mirroring the
// old hand-written testClient.
func newClient(t *testing.T, tag string) *MockClient {
	t.Helper()
	m := NewMockClient(gomock.NewController(t))
	m.EXPECT().Latest(gomock.Any()).Return(&Release{
		TagName: tag,
		HTMLURL: "https://example.org",
	}, nil).AnyTimes()
	return m
}

func TestInfo_IsDevelopment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		current string
		want    bool
	}{
		{"devel marker", "devel", true},
		{"unknown marker", "unknown", true},
		{"dirty suffix", "v1.2.3-dirty", true},
		{"go install pseudo-version", "v0.0.0-0.20251231235959-06c807842604", true},
		{"stable release", "v1.2.3", false},
		{"prerelease", "v1.2.3-beta.1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			info := Info{Current: tt.current}
			require.Equal(t, tt.want, info.IsDevelopment())
		})
	}
}

func TestGithubLatest_ContextCancelled(t *testing.T) {
	t.Parallel()

	// A pre-cancelled context makes the real github client fail before
	// any network I/O happens, so this exercises the error path
	// deterministically without reaching the network.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := Default.Latest(ctx)
	require.Error(t, err)
}
