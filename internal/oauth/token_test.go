package oauth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestToken_SetExpiresAt(t *testing.T) {
	t.Parallel()

	t.Run("uses ExpiresIn when positive", func(t *testing.T) {
		t.Parallel()
		tok := &Token{ExpiresIn: 3600}
		before := time.Now().Add(3600 * time.Second).Unix()
		tok.SetExpiresAt()
		after := time.Now().Add(3600 * time.Second).Unix()
		require.GreaterOrEqual(t, tok.ExpiresAt, before)
		require.LessOrEqual(t, tok.ExpiresAt, after)
	})

	t.Run("keeps existing ExpiresAt when ExpiresIn missing", func(t *testing.T) {
		t.Parallel()
		tok := &Token{ExpiresAt: 12345}
		tok.SetExpiresAt()
		require.Equal(t, int64(12345), tok.ExpiresAt)
	})

	t.Run("zeroes ExpiresAt when neither field usable", func(t *testing.T) {
		t.Parallel()
		tok := &Token{}
		tok.SetExpiresAt()
		require.Equal(t, int64(0), tok.ExpiresAt)
	})
}

func TestToken_IsExpired(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		expiresIn int
		expiresAt int64
		want      bool
	}{
		{
			name:      "far future expiry is not expired",
			expiresIn: 3600,
			expiresAt: time.Now().Add(time.Hour).Unix(),
			want:      false,
		},
		{
			name:      "past expiry is expired",
			expiresIn: 3600,
			expiresAt: time.Now().Add(-time.Minute).Unix(),
			want:      true,
		},
		{
			name:      "within default 30s buffer counts as expired",
			expiresIn: 0,
			expiresAt: time.Now().Add(10 * time.Second).Unix(),
			want:      true,
		},
		{
			name:      "within proportional buffer counts as expired",
			expiresIn: 1000, // buffer = 100s
			expiresAt: time.Now().Add(50 * time.Second).Unix(),
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tok := &Token{ExpiresIn: tt.expiresIn, ExpiresAt: tt.expiresAt}
			require.Equal(t, tt.want, tok.IsExpired())
		})
	}
}

func TestToken_SetExpiresIn(t *testing.T) {
	t.Parallel()

	tok := &Token{ExpiresAt: time.Now().Add(100 * time.Second).Unix()}
	tok.SetExpiresIn()
	require.InDelta(t, 100, tok.ExpiresIn, 2)
}

func TestTokenExchangeError_Error(t *testing.T) {
	t.Parallel()

	err := &TokenExchangeError{StatusCode: 400, Body: "bad request"}
	require.Equal(t, `token exchange failed: status 400 body "bad request"`, err.Error())
}

func TestTokenExchangeError_IsRefreshTokenRevoked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "revoked", body: `{"error":"revoked"}`, want: true},
		{name: "invalid_grant", body: `{"error":"invalid_grant"}`, want: true},
		{name: "other error", body: `{"error":"server_error"}`, want: false},
		{name: "empty body", body: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := &TokenExchangeError{Body: tt.body}
			require.Equal(t, tt.want, err.IsRefreshTokenRevoked())
		})
	}
}
