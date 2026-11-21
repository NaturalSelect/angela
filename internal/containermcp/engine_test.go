package containermcp

import (
	"net"
	"testing"

	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
)

func TestHijackedAttachment_ClosingTwiceIsNotAnError(t *testing.T) {
	path := shortSocket(t)
	var d net.Dialer
	conn, err := d.DialContext(t.Context(), "unix", path)
	require.NoError(t, err)

	att := &hijackedAttachment{resp: client.NewHijackedResponse(conn, "")}
	require.NoError(t, att.Close())
	require.NoError(t, att.CloseWrite(), "the SDK closes the writer after the reader already closed the session")
	require.NoError(t, att.Close())
}
