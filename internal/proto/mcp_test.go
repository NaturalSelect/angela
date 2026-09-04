package proto_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestMCPStateTextMarshaling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state proto.MCPState
		want  string
	}{
		{"disabled", proto.MCPStateDisabled, "disabled"},
		{"starting", proto.MCPStateStarting, "starting"},
		{"connected", proto.MCPStateConnected, "connected"},
		{"error", proto.MCPStateError, "error"},
		{"needs auth", proto.MCPStateNeedsAuth, "needs auth"},
		{"out of range", proto.MCPState(99), "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tc.want, tc.state.String())

			text, err := tc.state.MarshalText()
			require.NoError(t, err)
			require.Equal(t, tc.want, string(text))
		})
	}
}

func TestMCPStateUnmarshalText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		text    string
		want    proto.MCPState
		wantErr bool
	}{
		{"disabled", "disabled", proto.MCPStateDisabled, false},
		{"starting", "starting", proto.MCPStateStarting, false},
		{"connected", "connected", proto.MCPStateConnected, false},
		{"error", "error", proto.MCPStateError, false},
		{"needs auth", "needs auth", proto.MCPStateNeedsAuth, false},
		{"unknown", "bogus", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var got proto.MCPState
			err := got.UnmarshalText([]byte(tc.text))
			if tc.wantErr {
				require.Error(t, err)
				require.ErrorContains(t, err, tc.text)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestMCPEventTypeTextMarshaling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		typ  proto.MCPEventType
	}{
		{"state changed", proto.MCPEventStateChanged},
		{"tools list changed", proto.MCPEventToolsListChanged},
		{"prompts list changed", proto.MCPEventPromptsListChanged},
		{"resources list changed", proto.MCPEventResourcesListChanged},
		{"custom", proto.MCPEventType("custom_event")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			text, err := tc.typ.MarshalText()
			require.NoError(t, err)
			require.Equal(t, string(tc.typ), string(text))

			var got proto.MCPEventType
			require.NoError(t, got.UnmarshalText(text))
			require.Equal(t, tc.typ, got)
		})
	}
}

func TestMCPEventMarshalUnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		evt  proto.MCPEvent
	}{
		{
			name: "with error",
			evt: proto.MCPEvent{
				Type:          proto.MCPEventStateChanged,
				Name:          "server-1",
				State:         proto.MCPStateError,
				Error:         errors.New("connection refused"),
				ToolCount:     3,
				PromptCount:   2,
				ResourceCount: 1,
			},
		},
		{
			name: "without error",
			evt: proto.MCPEvent{
				Type:  proto.MCPEventToolsListChanged,
				Name:  "server-2",
				State: proto.MCPStateConnected,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tc.evt)
			require.NoError(t, err)

			var got proto.MCPEvent
			require.NoError(t, json.Unmarshal(data, &got))

			if tc.evt.Error != nil {
				require.EqualError(t, got.Error, tc.evt.Error.Error())
			} else {
				require.Nil(t, got.Error)
			}
			got.Error = nil
			want := tc.evt
			want.Error = nil
			require.Equal(t, want, got)
		})
	}
}

func TestMCPClientInfoMarshalUnmarshalJSON(t *testing.T) {
	t.Parallel()

	connectedAt := time.Date(2024, 3, 15, 10, 30, 0, 0, time.UTC)

	tests := []struct {
		name string
		info proto.MCPClientInfo
	}{
		{
			name: "with error",
			info: proto.MCPClientInfo{
				Name:          "server-1",
				State:         proto.MCPStateError,
				Error:         errors.New("boom"),
				ToolCount:     5,
				PromptCount:   1,
				ResourceCount: 2,
				ConnectedAt:   connectedAt,
			},
		},
		{
			name: "without error",
			info: proto.MCPClientInfo{
				Name:        "server-2",
				State:       proto.MCPStateConnected,
				ConnectedAt: connectedAt,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tc.info)
			require.NoError(t, err)

			var got proto.MCPClientInfo
			require.NoError(t, json.Unmarshal(data, &got))

			if tc.info.Error != nil {
				require.EqualError(t, got.Error, tc.info.Error.Error())
			} else {
				require.Nil(t, got.Error)
			}
			require.True(t, tc.info.ConnectedAt.Equal(got.ConnectedAt))
			require.Equal(t, tc.info.Name, got.Name)
			require.Equal(t, tc.info.State, got.State)
			require.Equal(t, tc.info.ToolCount, got.ToolCount)
			require.Equal(t, tc.info.PromptCount, got.PromptCount)
			require.Equal(t, tc.info.ResourceCount, got.ResourceCount)
		})
	}
}

func TestMCPEventUnmarshalJSONFieldTypeMismatch(t *testing.T) {
	t.Parallel()

	// Valid top-level JSON syntax so the outer json.Unmarshal actually
	// dispatches into MCPEvent.UnmarshalJSON, where the field type
	// mismatch (name expects a string) fails the aux decode step.
	var got proto.MCPEvent
	err := json.Unmarshal([]byte(`{"name":123}`), &got)
	require.Error(t, err)
}

func TestMCPClientInfoUnmarshalJSONFieldTypeMismatch(t *testing.T) {
	t.Parallel()

	var got proto.MCPClientInfo
	err := json.Unmarshal([]byte(`{"name":123}`), &got)
	require.Error(t, err)
}
