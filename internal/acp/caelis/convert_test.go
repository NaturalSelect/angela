package caelis

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	acp "github.com/caelis-labs/acp-go-sdk"
	"github.com/stretchr/testify/require"

	"github.com/NaturalSelect/angela/internal/acp/protocol"
)

func TestFromSessionUpdateWireFormat(t *testing.T) {
	t.Parallel()

	line := uint32(3)
	pending := protocol.ToolCallStatusPending
	completed := protocol.ToolCallStatusCompleted
	cases := []struct {
		name string
		in   protocol.SessionUpdate
		want string
	}{
		{
			name: "agent message",
			in:   protocol.AgentMessageChunk{MessageID: "m1", Content: protocol.TextBlock{Text: "hi"}},
			want: `{"sessionUpdate":"agent_message_chunk","messageId":"m1","content":{"type":"text","text":"hi"}}`,
		},
		{
			name: "agent thought without message id",
			in:   protocol.AgentThoughtChunk{Content: protocol.TextBlock{Text: "hmm"}},
			want: `{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"hmm"}}`,
		},
		{
			name: "user message with image",
			in: protocol.UserMessageChunk{
				MessageID: "m2",
				Content:   protocol.ImageBlock{MIMEType: "image/png", Data: []byte("hi")},
			},
			want: `{"sessionUpdate":"user_message_chunk","messageId":"m2","content":{"type":"image","mimeType":"image/png","data":"aGk="}}`,
		},
		{
			name: "tool call start omits raw input while input is streaming",
			in: protocol.ToolCallStart{
				ID: "t1", Name: "Read", Title: "Read a.go",
				Kind: protocol.ToolKindRead, Status: pending,
				Locations: []protocol.Location{{Path: "/a.go", Line: &line}},
			},
			want: `{"sessionUpdate":"tool_call","toolCallId":"t1","name":"Read","title":"Read a.go","kind":"read","status":"pending","locations":[{"path":"/a.go","line":3}]}`,
		},
		{
			name: "tool call start with raw input",
			in: protocol.ToolCallStart{
				ID: "t1", Title: "Bash", Kind: protocol.ToolKindExecute, Status: pending,
				RawInput: json.RawMessage(`{"command":"ls"}`),
			},
			want: `{"sessionUpdate":"tool_call","toolCallId":"t1","title":"Bash","kind":"execute","status":"pending","rawInput":{"command":"ls"}}`,
		},
		{
			name: "tool call update touches only set fields",
			in:   protocol.ToolCallUpdate{ID: "t1", Title: new("Edit a.go")},
			want: `{"sessionUpdate":"tool_call_update","toolCallId":"t1","title":"Edit a.go"}`,
		},
		{
			name: "tool call update with diff",
			in: protocol.ToolCallUpdate{
				ID: "t1", Status: &completed,
				Content: []protocol.ToolCallContent{
					protocol.ToolDiff{Path: "/a.go", OldText: new("old"), NewText: "new"},
				},
			},
			want: `{"sessionUpdate":"tool_call_update","toolCallId":"t1","status":"completed","content":[{"type":"diff","path":"/a.go","oldText":"old","newText":"new"}]}`,
		},
		{
			name: "diff of a new file has no old text",
			in: protocol.ToolCallUpdate{
				ID: "t1",
				Content: []protocol.ToolCallContent{
					protocol.ToolDiff{Path: "/new.go", NewText: "package x"},
				},
			},
			want: `{"sessionUpdate":"tool_call_update","toolCallId":"t1","content":[{"type":"diff","path":"/new.go","newText":"package x"}]}`,
		},
		{
			name: "diff of an existing empty file keeps empty old text",
			in: protocol.ToolCallUpdate{
				ID: "t1",
				Content: []protocol.ToolCallContent{
					protocol.ToolDiff{Path: "/empty.go", OldText: new(""), NewText: "package x"},
				},
			},
			want: `{"sessionUpdate":"tool_call_update","toolCallId":"t1","content":[{"type":"diff","path":"/empty.go","oldText":"","newText":"package x"}]}`,
		},
		{
			name: "tool call text content",
			in: protocol.ToolCallUpdate{
				ID: "t1",
				Content: []protocol.ToolCallContent{
					protocol.ToolContent{Block: protocol.TextBlock{Text: "out"}},
				},
			},
			want: `{"sessionUpdate":"tool_call_update","toolCallId":"t1","content":[{"type":"content","content":{"type":"text","text":"out"}}]}`,
		},
		{
			name: "plan",
			in: protocol.Plan{Entries: []protocol.PlanEntry{
				{Content: "c", Priority: protocol.PlanPriorityMedium, Status: protocol.PlanStatusPending},
			}},
			want: `{"sessionUpdate":"plan","entries":[{"content":"c","priority":"medium","status":"pending"}]}`,
		},
		{
			name: "session info",
			in:   protocol.SessionInfoUpdate{Title: new("Fix tests")},
			want: `{"sessionUpdate":"session_info_update","title":"Fix tests"}`,
		},
		{
			name: "usage with cost",
			in: protocol.UsageUpdate{
				Used: 10, Size: 100,
				Cost: &protocol.Cost{Amount: 0.5, Currency: "USD"},
			},
			want: `{"sessionUpdate":"usage_update","used":10,"size":100,"cost":{"amount":0.5,"currency":"USD"}}`,
		},
		{
			name: "usage without cost",
			in:   protocol.UsageUpdate{Used: 10, Size: 100},
			want: `{"sessionUpdate":"usage_update","used":10,"size":100}`,
		},
		{
			name: "config options",
			in: protocol.ConfigOptionUpdate{ConfigOptions: []protocol.ConfigOption{{
				ID: "model", Name: "Model", Category: protocol.ConfigCategoryModel, CurrentValue: "a",
				Values: []protocol.ConfigValue{{Value: "a", Name: "A"}},
			}}},
			want: `{"sessionUpdate":"config_option_update","configOptions":[{"type":"select","id":"model","name":"Model","category":"model","currentValue":"a","options":[{"value":"a","name":"A"}]}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := fromSessionUpdate(tc.in)
			require.NoError(t, err)
			encoded, err := json.Marshal(got)
			require.NoError(t, err)
			require.JSONEq(t, tc.want, string(encoded))
		})
	}
}

func TestFromConfigOptionsWireFormat(t *testing.T) {
	t.Parallel()

	got := fromConfigOptions([]protocol.ConfigOption{{
		ID: "model", Name: "Model", Description: "d",
		Category: protocol.ConfigCategoryModel, CurrentValue: "a",
		Values: []protocol.ConfigValue{
			{Value: "a", Name: "A", Description: "da"},
			{Value: "b", Name: "B"},
		},
	}})
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, `[{
		"type":"select","id":"model","name":"Model","description":"d","category":"model",
		"currentValue":"a",
		"options":[{"value":"a","name":"A","description":"da"},{"value":"b","name":"B"}]
	}]`, string(encoded))
}

func TestFromListSessions(t *testing.T) {
	t.Parallel()

	updated := time.Date(2026, 10, 25, 9, 30, 0, 0, time.FixedZone("x", 8*3600))
	got := fromListSessions(protocol.ListSessionsResponse{
		Sessions: []protocol.SessionInfo{
			{SessionID: "s1", Cwd: "/p", Title: "T", UpdatedAt: updated},
			{SessionID: "s2", Cwd: "/p"},
		},
		NextCursor: "next",
	})
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"sessions":[
			{"sessionId":"s1","cwd":"/p","title":"T","updatedAt":"2026-10-25T01:30:00Z"},
			{"sessionId":"s2","cwd":"/p"}
		],
		"nextCursor":"next"
	}`, string(encoded))
}

func TestFromInitializeResponseDeclaresOnlyEnabledCapabilities(t *testing.T) {
	t.Parallel()

	full := fromInitializeResponse(protocol.InitializeResponse{
		ProtocolVersion: 1, AgentName: "angela", AgentVersion: "v1",
		LoadSession: true, ListSessions: true, DeleteSessions: true,
		PromptImage: true, PromptEmbeddedContext: true,
	})
	require.True(t, full.AgentCapabilities.LoadSession)
	require.NotNil(t, full.AgentCapabilities.SessionCapabilities.List)
	require.NotNil(t, full.AgentCapabilities.SessionCapabilities.Delete)
	require.True(t, full.AgentCapabilities.PromptCapabilities.Image)
	require.True(t, full.AgentCapabilities.PromptCapabilities.EmbeddedContext)
	require.Equal(t, "angela", full.AgentInfo.Name)

	bare := fromInitializeResponse(protocol.InitializeResponse{ProtocolVersion: 1})
	require.False(t, bare.AgentCapabilities.LoadSession)
	require.Nil(t, bare.AgentCapabilities.SessionCapabilities.List)
	require.Nil(t, bare.AgentCapabilities.SessionCapabilities.Delete)
	require.Nil(t, bare.AgentCapabilities.SessionCapabilities.Resume)
	require.Nil(t, bare.AgentCapabilities.SessionCapabilities.Close)
	require.False(t, bare.AgentCapabilities.PromptCapabilities.Audio)
}

func TestToInitializeReadsElicitationForm(t *testing.T) {
	t.Parallel()

	parse := func(t *testing.T, raw string) acp.InitializeRequest {
		t.Helper()
		var req acp.InitializeRequest
		require.NoError(t, json.Unmarshal([]byte(raw), &req))
		return req
	}
	with := toInitialize(parse(t, `{"protocolVersion":1,"clientCapabilities":{"elicitation":{"form":{}}}}`))
	require.True(t, with.ElicitationForm)
	require.Equal(t, 1, with.ProtocolVersion)

	urlOnly := toInitialize(parse(t, `{"protocolVersion":1,"clientCapabilities":{"elicitation":{"url":{}}}}`))
	require.False(t, urlOnly.ElicitationForm)

	none := toInitialize(parse(t, `{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":true}}}`))
	require.False(t, none.ElicitationForm)
}

func TestToPrompt(t *testing.T) {
	t.Parallel()

	parse := func(t *testing.T, blocks string) acp.PromptRequest {
		t.Helper()
		var req acp.PromptRequest
		require.NoError(t, json.Unmarshal([]byte(`{"sessionId":"s1","prompt":`+blocks+`}`), &req))
		return req
	}

	t.Run("all supported blocks", func(t *testing.T) {
		t.Parallel()
		got, err := toPrompt(parse(t, `[
			{"type":"text","text":"fix it"},
			{"type":"image","mimeType":"image/png","data":"aGk="},
			{"type":"resource_link","name":"a.go","uri":"file:///p/a.go","mimeType":"text/x-go"},
			{"type":"resource","resource":{"uri":"file:///p/b.go","mimeType":"text/x-go","text":"package b"}},
			{"type":"resource","resource":{"uri":"file:///p/c.bin","blob":"AQI="}}
		]`))
		require.NoError(t, err)
		require.Equal(t, "s1", got.SessionID)
		require.Equal(t, []protocol.ContentBlock{
			protocol.TextBlock{Text: "fix it"},
			protocol.ImageBlock{MIMEType: "image/png", Data: []byte("hi")},
			protocol.ResourceLinkBlock{URI: "file:///p/a.go", Name: "a.go", MIMEType: "text/x-go"},
			protocol.ResourceBlock{URI: "file:///p/b.go", MIMEType: "text/x-go", Text: "package b"},
			protocol.ResourceBlock{URI: "file:///p/c.bin", Blob: []byte{1, 2}},
		}, got.Prompt)
	})

	rejected := map[string]string{
		"audio":                 `[{"type":"audio","mimeType":"audio/wav","data":"aGk="}]`,
		"image bad base64":      `[{"type":"image","mimeType":"image/png","data":"!!!"}]`,
		"resource bad base64":   `[{"type":"resource","resource":{"uri":"file:///x","blob":"!!!"}}]`,
		"image after good text": `[{"type":"text","text":"ok"},{"type":"image","mimeType":"image/png","data":"!!!"}]`,
	}
	for name, blocks := range rejected {
		t.Run("rejects "+name, func(t *testing.T) {
			t.Parallel()
			_, err := toPrompt(parse(t, blocks))
			var pe protocol.Error
			require.ErrorAs(t, err, &pe)
			require.Equal(t, protocol.CodeInvalidParams, pe.Code)
		})
	}
}

func TestToSetConfigOptionRejectsBoolean(t *testing.T) {
	t.Parallel()

	var req acp.SetSessionConfigOptionRequest
	require.NoError(t, json.Unmarshal(
		[]byte(`{"sessionId":"s1","configId":"thinking","type":"boolean","value":true}`), &req))
	_, err := toSetConfigOption(req)
	var pe protocol.Error
	require.ErrorAs(t, err, &pe)
	require.Equal(t, protocol.CodeInvalidParams, pe.Code)

	require.NoError(t, json.Unmarshal(
		[]byte(`{"sessionId":"s1","configId":"model","value":"a:b"}`), &req))
	got, err := toSetConfigOption(req)
	require.NoError(t, err)
	require.Equal(t, protocol.SetConfigOptionRequest{SessionID: "s1", ConfigID: "model", Value: "a:b"}, got)
}

func TestFromElicitationRequestSchema(t *testing.T) {
	t.Parallel()

	got := fromElicitationRequest(protocol.ElicitationRequest{
		SessionID: "s1", ToolCallID: "tc1", Message: "Pick",
		Fields: []protocol.FormField{
			{Name: "free", Title: "Free", Kind: protocol.FieldKindText, Required: true},
			{Name: "ok", Title: "OK?", Description: "sure?", Kind: protocol.FieldKindBoolean},
			{
				Name: "one", Title: "One", Kind: protocol.FieldKindSingleSelect,
				Options: []protocol.FormOption{{Value: "a", Title: "A"}, {Value: "b", Title: "B"}},
			},
			{
				Name: "many", Title: "Many", Kind: protocol.FieldKindMultiSelect,
				Options: []protocol.FormOption{{Value: "x", Title: "X"}},
			},
		},
	})
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"mode":"form","message":"Pick","sessionId":"s1","toolCallId":"tc1",
		"requestedSchema":{
			"type":"object",
			"required":["free"],
			"properties":{
				"free":{"type":"string","title":"Free"},
				"ok":{"type":"boolean","title":"OK?","description":"sure?"},
				"one":{"type":"string","title":"One","oneOf":[
					{"const":"a","title":"A"},{"const":"b","title":"B"}]},
				"many":{"type":"array","title":"Many","items":{"anyOf":[{"const":"x","title":"X"}]}}
			}
		}
	}`, string(encoded))
}

func TestToElicitationResponse(t *testing.T) {
	t.Parallel()

	accepted := toElicitationResponse(acp.CreateElicitationResponse{
		Accept: &acp.CreateElicitationAccept{Action: "accept", Content: map[string]any{"a": "b"}},
	})
	require.Equal(t, protocol.ElicitationResponse{
		Action: protocol.ElicitActionAccept, Values: map[string]any{"a": "b"},
	}, accepted)

	declined := toElicitationResponse(acp.CreateElicitationResponse{
		Decline: &acp.CreateElicitationDecline{Action: "decline"},
	})
	require.Equal(t, protocol.ElicitActionDecline, declined.Action)

	cancelled := toElicitationResponse(acp.CreateElicitationResponse{
		Cancel: &acp.CreateElicitationCancel{Action: "cancel"},
	})
	require.Equal(t, protocol.ElicitActionCancel, cancelled.Action)

	unknown := toElicitationResponse(acp.CreateElicitationResponse{})
	require.Equal(t, protocol.ElicitActionCancel, unknown.Action)
}

func TestToPermissionResponse(t *testing.T) {
	t.Parallel()

	selected := toPermissionResponse(acp.RequestPermissionResponse{
		Outcome: acp.NewRequestPermissionOutcomeSelected("allow_once"),
	})
	require.Equal(t, protocol.PermissionResponse{OptionID: "allow_once"}, selected)

	cancelled := toPermissionResponse(acp.RequestPermissionResponse{
		Outcome: acp.NewRequestPermissionOutcomeCancelled(),
	})
	require.Equal(t, protocol.PermissionResponse{Cancelled: true}, cancelled)
}

func TestFromPermissionRequest(t *testing.T) {
	t.Parallel()

	got, err := fromPermissionRequest(protocol.PermissionRequest{
		SessionID: "s1",
		ToolCall: protocol.ToolCallUpdate{
			ID: "t1",
			Content: []protocol.ToolCallContent{
				protocol.ToolDiff{Path: "/a.go", OldText: new("o"), NewText: "n"},
			},
		},
		Options: []protocol.PermissionOption{
			{ID: "allow", Name: "Allow", Kind: protocol.PermissionKindAllowOnce},
			{ID: "deny", Name: "Deny", Kind: protocol.PermissionKindRejectOnce},
		},
	})
	require.NoError(t, err)
	encoded, err := json.Marshal(got)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"sessionId":"s1",
		"toolCall":{"toolCallId":"t1","content":[{"type":"diff","path":"/a.go","oldText":"o","newText":"n"}]},
		"options":[
			{"optionId":"allow","name":"Allow","kind":"allow_once"},
			{"optionId":"deny","name":"Deny","kind":"reject_once"}
		]
	}`, string(encoded))
}

func TestToRequestError(t *testing.T) {
	t.Parallel()

	require.NoError(t, toRequestError(nil))

	cases := []struct {
		name     string
		in       error
		wantCode int
		wantMsg  string
	}{
		{"invalid params", protocol.Error{Code: protocol.CodeInvalidParams, Message: "bad"}, -32602, "bad"},
		{"not found", protocol.Error{Code: protocol.CodeNotFound, Message: "gone"}, -32002, "gone"},
		{"internal", protocol.Error{Code: protocol.CodeInternal, Message: "boom"}, -32603, "boom"},
		{"wrapped protocol error", errors.Join(errors.New("ctx"), protocol.Error{Code: protocol.CodeNotFound, Message: "gone"}), -32002, "gone"},
		{"plain error", errors.New("plain"), -32603, "plain"},
		{"context canceled", context.Canceled, -32603, "context canceled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var re *acp.RequestError
			require.ErrorAs(t, toRequestError(tc.in), &re)
			require.Equal(t, tc.wantCode, re.Code)
			require.Equal(t, tc.wantMsg, re.Message)
		})
	}
}
