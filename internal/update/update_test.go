package update

import (
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
