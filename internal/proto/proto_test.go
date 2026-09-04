package proto_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/lsp"
	"github.com/NaturalSelect/angela/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestAgentInfoIsZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		info proto.AgentInfo
		want bool
	}{
		{"zero value", proto.AgentInfo{}, true},
		{"busy", proto.AgentInfo{IsBusy: true}, false},
		{"ready", proto.AgentInfo{IsReady: true}, false},
		{"model id set", proto.AgentInfo{Model: config.ProviderModel{Model: catwalk.Model{ID: "gpt-4o"}}}, false},
		{
			name: "model_cfg populated but model id empty still counts as zero",
			info: proto.AgentInfo{ModelCfg: config.SelectedModel{Model: "gpt-4o", Provider: "openai"}},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.info.IsZero())
		})
	}
}

func TestAgentSessionIsZero(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sess proto.AgentSession
		want bool
	}{
		{"zero value", proto.AgentSession{}, true},
		{"busy", proto.AgentSession{IsBusy: true}, false},
		{"branch", proto.AgentSession{IsBranch: true}, false},
		{"id set", proto.AgentSession{Session: proto.Session{ID: "sess-1"}}, false},
		{
			name: "other session fields populated but id/busy/branch zero still counts as zero",
			sess: proto.AgentSession{Session: proto.Session{Title: "some title", MessageCount: 4}},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.sess.IsZero())
		})
	}
}

func TestPermissionActionTextMarshaling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		action proto.PermissionAction
	}{
		{"allow", proto.PermissionAllow},
		{"allow session", proto.PermissionAllowForSession},
		{"deny", proto.PermissionDeny},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			text, err := tc.action.MarshalText()
			require.NoError(t, err)
			require.Equal(t, string(tc.action), string(text))

			var got proto.PermissionAction
			require.NoError(t, got.UnmarshalText(text))
			require.Equal(t, tc.action, got)
		})
	}
}

func TestLSPEventTypeTextMarshaling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		typ  proto.LSPEventType
	}{
		{"state changed", proto.LSPEventStateChanged},
		{"diagnostics changed", proto.LSPEventDiagnosticsChanged},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			text, err := tc.typ.MarshalText()
			require.NoError(t, err)
			require.Equal(t, string(tc.typ), string(text))

			var got proto.LSPEventType
			require.NoError(t, got.UnmarshalText(text))
			require.Equal(t, tc.typ, got)
		})
	}
}

func TestLSPEventMarshalUnmarshalJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		evt  proto.LSPEvent
	}{
		{
			name: "with error",
			evt: proto.LSPEvent{
				Type:            proto.LSPEventStateChanged,
				Name:            "gopls",
				State:           lsp.StateError,
				Error:           errors.New("crashed"),
				DiagnosticCount: 4,
			},
		},
		{
			name: "without error",
			evt: proto.LSPEvent{
				Type:  proto.LSPEventDiagnosticsChanged,
				Name:  "gopls",
				State: lsp.StateReady,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tc.evt)
			require.NoError(t, err)

			var got proto.LSPEvent
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

func TestLSPClientInfoMarshalUnmarshalJSON(t *testing.T) {
	t.Parallel()

	connectedAt := time.Date(2024, 6, 1, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		info proto.LSPClientInfo
	}{
		{
			name: "with error",
			info: proto.LSPClientInfo{
				Name:            "gopls",
				State:           lsp.StateError,
				Error:           errors.New("failed to start"),
				DiagnosticCount: 7,
				ConnectedAt:     connectedAt,
			},
		},
		{
			name: "without error",
			info: proto.LSPClientInfo{
				Name:        "gopls",
				State:       lsp.StateReady,
				ConnectedAt: connectedAt,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			data, err := json.Marshal(tc.info)
			require.NoError(t, err)

			var got proto.LSPClientInfo
			require.NoError(t, json.Unmarshal(data, &got))

			if tc.info.Error != nil {
				require.EqualError(t, got.Error, tc.info.Error.Error())
			} else {
				require.Nil(t, got.Error)
			}
			require.True(t, tc.info.ConnectedAt.Equal(got.ConnectedAt))
			require.Equal(t, tc.info.Name, got.Name)
			require.Equal(t, tc.info.State, got.State)
			require.Equal(t, tc.info.DiagnosticCount, got.DiagnosticCount)
		})
	}
}

func TestLSPEventUnmarshalJSONFieldTypeMismatch(t *testing.T) {
	t.Parallel()

	// Valid top-level JSON syntax so the outer json.Unmarshal actually
	// dispatches into LSPEvent.UnmarshalJSON, where the field type
	// mismatch (name expects a string) fails the aux decode step.
	var got proto.LSPEvent
	err := json.Unmarshal([]byte(`{"name":123}`), &got)
	require.Error(t, err)
}

func TestLSPClientInfoUnmarshalJSONFieldTypeMismatch(t *testing.T) {
	t.Parallel()

	var got proto.LSPClientInfo
	err := json.Unmarshal([]byte(`{"name":123}`), &got)
	require.Error(t, err)
}
