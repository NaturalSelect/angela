package update

import (
	"context"
	"net/http"
	"net/http/httptest"
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

	t.Run("current is a pre-release ahead of the latest stable release", func(t *testing.T) {
		// The latest GitHub release only tracks the newest non-prerelease
		// tag, so it can lag behind a pre-release of a not-yet-released
		// version. Current being numerically newer must not be reported as
		// an available update.
		info, err := Check(t.Context(), "v0.0.2-rc1", newClient(t, "v0.0.1"))
		require.NoError(t, err)
		require.NotNil(t, info)
		require.False(t, info.Available())
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

func TestGetJSON_SendsToken(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"no token", nil, ""},
		{"GITHUB_TOKEN", map[string]string{"GITHUB_TOKEN": "abc"}, "Bearer abc"},
		{"GH_TOKEN", map[string]string{"GH_TOKEN": "def"}, "Bearer def"},
		{"GITHUB_TOKEN wins", map[string]string{"GITHUB_TOKEN": "abc", "GH_TOKEN": "def"}, "Bearer abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("GITHUB_TOKEN", "")
			t.Setenv("GH_TOKEN", "")
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			var got string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got = r.Header.Get("Authorization")
				_, _ = w.Write([]byte(`{}`))
			}))
			t.Cleanup(srv.Close)

			var v map[string]any
			require.NoError(t, getJSON(t.Context(), srv.URL, &v))
			require.Equal(t, tt.want, got)
		})
	}
}

func TestGetJSON_RateLimited(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
	}))
	t.Cleanup(srv.Close)

	var v map[string]any
	err := getJSON(t.Context(), srv.URL, &v)
	require.ErrorContains(t, err, "rate limit exceeded")
	require.ErrorContains(t, err, "GITHUB_TOKEN")
}

func TestGetJSON_ForbiddenWithoutRateLimit(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`forbidden`))
	}))
	t.Cleanup(srv.Close)

	var v map[string]any
	err := getJSON(t.Context(), srv.URL, &v)
	require.ErrorContains(t, err, "status 403")
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
