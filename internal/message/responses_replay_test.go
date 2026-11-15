package message

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/openai"
	"github.com/stretchr/testify/require"
)

type responsesRecorder struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (r *responsesRecorder) requests() []map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]map[string]any(nil), r.bodies...)
}

func newResponsesServer(t *testing.T, events string) (*httptest.Server, *responsesRecorder) {
	t.Helper()

	rec := &responsesRecorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rec.mu.Lock()
		rec.bodies = append(rec.bodies, body)
		rec.mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, events)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func sseEvent(name, data string) string {
	return fmt.Sprintf("event: %s\ndata: %s\n\n", name, data)
}

func newResponsesModel(t *testing.T, baseURL string) fantasy.LanguageModel {
	t.Helper()

	provider, err := openai.New(
		openai.WithAPIKey("test-key"),
		openai.WithBaseURL(baseURL),
		openai.WithUseResponsesAPI(),
	)
	require.NoError(t, err)
	model, err := provider.LanguageModel(t.Context(), "gpt-5")
	require.NoError(t, err)
	return model
}

// NOTE: Mirrors the reasoning callbacks in the agent loop.
func applyReasoningStream(t *testing.T, m *Message, stream fantasy.StreamResponse) {
	t.Helper()

	for part := range stream {
		require.NotEqual(t, fantasy.StreamPartTypeError, part.Type, "%v", part.Error)
		switch part.Type {
		case fantasy.StreamPartTypeReasoningStart:
			m.AppendReasoningContent("")
		case fantasy.StreamPartTypeReasoningDelta:
			m.AppendReasoningContent(part.Delta)
		case fantasy.StreamPartTypeReasoningEnd:
			if meta, ok := part.ProviderMetadata[openai.Name].(*openai.ResponsesReasoningMetadata); ok {
				m.SetReasoningResponsesData(meta)
			}
			m.FinishThinking()
		}
	}
}

func TestResponsesReasoningSurvivesStorageAndReplay(t *testing.T) {
	t.Parallel()

	events := sseEvent("response.output_item.added", `{"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","summary":[]}}`) +
		sseEvent("response.output_item.done", `{"type":"response.output_item.done","output_index":0,"item":{"id":"rs_1","type":"reasoning","encrypted_content":"enc-final","summary":[{"type":"summary_text","text":"plan"}]}}`) +
		sseEvent("response.completed", `{"type":"response.completed","response":{"id":"resp_1","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`)
	srv, rec := newResponsesServer(t, events)
	model := newResponsesModel(t, srv.URL)

	user := Message{Role: User, Parts: []ContentPart{TextContent{Text: "list files"}}}
	stream, err := model.Stream(t.Context(), fantasy.Call{Prompt: fantasy.Prompt(user.ToAIMessage())})
	require.NoError(t, err)

	assistant := Message{Role: Assistant}
	applyReasoningStream(t, &assistant, stream)
	assistant.Parts = append(assistant.Parts, ToolCall{ID: "call_1", Name: "ls", Input: `{}`, Finished: true})

	// Same trip the message takes through SQLite.
	data, err := marshalParts(assistant.Parts)
	require.NoError(t, err)
	stored, err := unmarshalParts(data)
	require.NoError(t, err)
	assistant.Parts = stored

	toolMsg := Message{Role: Tool, Parts: []ContentPart{ToolResult{ToolCallID: "call_1", Name: "ls", Content: "a.go"}}}
	prompt := fantasy.Prompt{}
	prompt = append(prompt, user.ToAIMessage()...)
	prompt = append(prompt, assistant.ToAIMessage()...)
	prompt = append(prompt, toolMsg.ToAIMessage()...)

	stream, err = model.Stream(t.Context(), fantasy.Call{Prompt: prompt})
	require.NoError(t, err)
	for range stream {
	}

	reqs := rec.requests()
	require.Len(t, reqs, 2)
	require.Equal(t, false, reqs[1]["store"], "stateless requests must replay reasoning inline")

	input, ok := reqs[1]["input"].([]any)
	require.True(t, ok)

	var types []string
	var reasoning map[string]any
	for _, raw := range input {
		item := raw.(map[string]any)
		typ, _ := item["type"].(string)
		if typ == "" {
			typ = "message"
		}
		types = append(types, typ)
		if typ == "reasoning" {
			reasoning = item
		}
	}
	require.Equal(t, []string{"message", "reasoning", "function_call", "function_call_output"}, types)
	require.Equal(t, "rs_1", reasoning["id"])
	require.Equal(t, "enc-final", reasoning["encrypted_content"])
	require.Equal(t, []any{map[string]any{"type": "summary_text", "text": "plan"}}, reasoning["summary"])
}
