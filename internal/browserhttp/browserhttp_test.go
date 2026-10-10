package browserhttp

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewClient(t *testing.T) {
	t.Parallel()

	client := NewClient(Options{Timeout: 42 * time.Second})
	require.Equal(t, 42*time.Second, client.Timeout)

	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok, "expected an *http.Transport")
	require.Equal(t, 100, transport.MaxIdleConns)
	require.Equal(t, 10, transport.MaxIdleConnsPerHost)
	require.Equal(t, 90*time.Second, transport.IdleConnTimeout)
}

func TestNewClientDefaultTimeout(t *testing.T) {
	t.Parallel()

	require.Equal(t, DefaultTimeout, NewClient(Options{}).Timeout)
}
