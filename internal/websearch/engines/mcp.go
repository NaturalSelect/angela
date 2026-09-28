package engines

import (
	"encoding/json"
	"errors"
	"strings"
)

// errNoSSEPayload is returned by parseSSEData when no `data:` line in
// the response carried a non-empty JSON payload.
var errNoSSEPayload = errors.New("no data payload found in SSE response")

// parseSSEData extracts the first usable JSON payload from a
// `data: <json>` Server-Sent-Events-formatted response body — the
// shape MCP endpoints (e.g. Exa's) reply with instead of a plain JSON
// body. It skips blank lines and any other SSE fields (event:, id:,
// comments), and keeps looking until a `data:` line both parses and
// decodes into a non-empty payload, since some MCP servers interleave
// keep-alive events with the real message. Shared so additional
// MCP-based engines can reuse it without repeating the framing logic.
func parseSSEData(body []byte, out any) error {
	for line := range strings.SplitSeq(string(body), "\n") {
		payload, ok := strings.CutPrefix(strings.TrimSpace(line), "data:")
		if !ok {
			continue
		}
		payload = strings.TrimSpace(payload)
		if payload == "" {
			continue
		}
		if err := json.Unmarshal([]byte(payload), out); err != nil {
			continue
		}
		return nil
	}
	return errNoSSEPayload
}
