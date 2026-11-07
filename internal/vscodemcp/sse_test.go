package vscodemcp

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
)

type tapped struct {
	method string
	params string
}

func readThroughTap(t *testing.T, r io.Reader) ([]tapped, string) {
	t.Helper()
	var got []tapped
	tap := &sseTap{
		body: io.NopCloser(r),
		emit: func(method string, params json.RawMessage) {
			got = append(got, tapped{method, string(params)})
		},
	}
	out, err := io.ReadAll(tap)
	require.NoError(t, err)
	return got, string(out)
}

func TestSSETap_PassesBodyThroughUnchanged(t *testing.T) {
	t.Parallel()

	body := "event: message\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"selection_changed\",\"params\":{\"a\":1}}\n\n"
	_, out := readThroughTap(t, strings.NewReader(body))
	require.Equal(t, body, out)
}

func TestSSETap_Events(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want []tapped
	}{
		{
			name: "notification",
			body: `data: {"jsonrpc":"2.0","method":"selection_changed","params":{"a":1}}` + "\n\n",
			want: []tapped{{"selection_changed", `{"a":1}`}},
		},
		{
			name: "crlf line endings",
			body: "data: {\"jsonrpc\":\"2.0\",\"method\":\"x\",\"params\":{}}\r\n\r\n",
			want: []tapped{{"x", `{}`}},
		},
		{
			name: "data without a space after the colon",
			body: `data:{"jsonrpc":"2.0","method":"x","params":{}}` + "\n\n",
			want: []tapped{{"x", `{}`}},
		},
		{
			name: "data split over several lines",
			body: "data: {\"jsonrpc\":\"2.0\",\ndata: \"method\":\"x\",\ndata: \"params\":{}}\n\n",
			want: []tapped{{"x", `{}`}},
		},
		{
			name: "event and id fields and comments are ignored",
			body: ": keep-alive\nevent: message\nid: 7\ndata: {\"jsonrpc\":\"2.0\",\"method\":\"x\",\"params\":{}}\n\n",
			want: []tapped{{"x", `{}`}},
		},
		{
			name: "several events",
			body: `data: {"jsonrpc":"2.0","method":"a","params":{}}` + "\n\n" +
				`data: {"jsonrpc":"2.0","method":"b","params":{}}` + "\n\n",
			want: []tapped{{"a", `{}`}, {"b", `{}`}},
		},
		{
			name: "protocol notifications stay with the SDK",
			body: `data: {"jsonrpc":"2.0","method":"notifications/tools/list_changed"}` + "\n\n",
		},
		{
			name: "responses are not notifications",
			body: `data: {"jsonrpc":"2.0","id":1,"result":{}}` + "\n\n",
		},
		{
			name: "server requests are not notifications",
			body: `data: {"jsonrpc":"2.0","id":1,"method":"roots/list"}` + "\n\n",
		},
		{
			name: "null id still counts as a notification",
			body: `data: {"jsonrpc":"2.0","id":null,"method":"x","params":{}}` + "\n\n",
			want: []tapped{{"x", `{}`}},
		},
		{
			name: "garbage is skipped",
			body: "data: not json\n\n" + `data: {"jsonrpc":"2.0","method":"x","params":{}}` + "\n\n",
			want: []tapped{{"x", `{}`}},
		},
		{
			name: "event never terminated is not reported",
			body: `data: {"jsonrpc":"2.0","method":"x","params":{}}` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, _ := readThroughTap(t, strings.NewReader(tt.body))
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSSETap_ChunkBoundariesDoNotMatter(t *testing.T) {
	t.Parallel()

	body := `data: {"jsonrpc":"2.0","method":"a","params":{"n":1}}` + "\n\n" +
		`data: {"jsonrpc":"2.0","method":"b","params":{"n":2}}` + "\n\n"

	// One byte at a time puts a boundary in every possible place.
	got, out := readThroughTap(t, iotest.OneByteReader(strings.NewReader(body)))
	require.Equal(t, body, out)
	require.Equal(t, []tapped{{"a", `{"n":1}`}, {"b", `{"n":2}`}}, got)
}

func TestSSETap_OversizedEventIsDropped(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("x", maxEventSize+1)
	body := `data: {"jsonrpc":"2.0","method":"big","params":"` + huge + `"}` + "\n\n" +
		`data: {"jsonrpc":"2.0","method":"after","params":{}}` + "\n\n"

	got, _ := readThroughTap(t, strings.NewReader(body))
	require.Equal(t, []tapped{{"after", `{}`}}, got)
}
