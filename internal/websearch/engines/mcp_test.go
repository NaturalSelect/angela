package engines

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSSEData_FindsFirstDataLine(t *testing.T) {
	t.Parallel()
	body := []byte("event: message\ndata: {\"foo\":\"bar\"}\n\n")
	var out struct {
		Foo string `json:"foo"`
	}
	err := parseSSEData(body, &out)
	require.NoError(t, err)
	require.Equal(t, "bar", out.Foo)
}

func TestParseSSEData_SkipsBlankAndUnparseableLines(t *testing.T) {
	t.Parallel()
	body := []byte("id: 1\ndata: \nevent: message\ndata: not json\ndata: {\"foo\":\"baz\"}\n")
	var out struct {
		Foo string `json:"foo"`
	}
	err := parseSSEData(body, &out)
	require.NoError(t, err)
	require.Equal(t, "baz", out.Foo)
}

func TestParseSSEData_ErrorsWhenNoDataLine(t *testing.T) {
	t.Parallel()
	var out map[string]any
	err := parseSSEData([]byte("event: message\n"), &out)
	require.ErrorIs(t, err, errNoSSEPayload)
}
