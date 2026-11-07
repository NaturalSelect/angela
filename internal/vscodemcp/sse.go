package vscodemcp

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// maxEventSize caps how much of one server-sent event sseTap buffers. It
// matches the 10mb body limit VS Code's own server applies, so anything
// larger is something VS Code itself would not have produced.
const maxEventSize = 8 << 20

// sseTap passes a text/event-stream body through unchanged while
// reporting every JSON-RPC notification that flows by. The MCP SDK
// client dispatches only the notification methods the MCP spec defines,
// so VS Code's own (selection_changed and friends) would otherwise be
// dropped without anyone seeing them.
//
// Methods under "notifications/" are the protocol's own and stay with
// the SDK. A Read call is the only place emit runs, so emit must not
// block.
type sseTap struct {
	body io.ReadCloser
	emit func(method string, params json.RawMessage)

	line     []byte
	data     []byte
	overflow bool
}

func (t *sseTap) Read(p []byte) (int, error) {
	n, err := t.body.Read(p)
	t.feed(p[:n])
	return n, err
}

func (t *sseTap) Close() error { return t.body.Close() }

// feed consumes the next chunk of the stream. Chunks can end anywhere,
// so a partial line is carried over to the next call.
func (t *sseTap) feed(chunk []byte) {
	for len(chunk) > 0 {
		i := bytes.IndexByte(chunk, '\n')
		if i < 0 {
			t.appendLine(chunk)
			return
		}
		t.appendLine(chunk[:i])
		t.endLine()
		chunk = chunk[i+1:]
	}
}

func (t *sseTap) appendLine(b []byte) {
	if len(t.line)+len(b) > maxEventSize {
		t.overflow = true
		return
	}
	t.line = append(t.line, b...)
}

func (t *sseTap) endLine() {
	line := bytes.TrimSuffix(t.line, []byte("\r"))
	defer func() { t.line = t.line[:0] }()

	if len(line) == 0 {
		t.dispatch()
		return
	}
	// A line starting with a colon is a comment, used as a keep-alive.
	if line[0] == ':' {
		return
	}

	field, value, _ := strings.Cut(string(line), ":")
	if field != "data" {
		return
	}
	value = strings.TrimPrefix(value, " ")
	if len(t.data)+len(value)+1 > maxEventSize {
		t.overflow = true
		return
	}
	if len(t.data) > 0 {
		t.data = append(t.data, '\n')
	}
	t.data = append(t.data, value...)
}

// dispatch ends the event being assembled, reporting it if it is a
// notification.
func (t *sseTap) dispatch() {
	data, overflow := t.data, t.overflow
	t.data, t.overflow = t.data[:0], false
	if overflow || len(data) == 0 {
		return
	}

	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}
	isNotification := len(msg.ID) == 0 || string(msg.ID) == "null"
	if msg.Method == "" || !isNotification || strings.HasPrefix(msg.Method, "notifications/") {
		return
	}
	t.emit(msg.Method, msg.Params)
}
