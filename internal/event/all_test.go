package event

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/posthog/posthog-go"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

// withMockClient installs a mock PostHog client and a fixed distinct ID for
// the duration of the test, restoring both package globals afterward.
func withMockClient(t *testing.T) *MockPosthogClient {
	t.Helper()
	originalClient := client
	originalDistinctId := distinctId
	t.Cleanup(func() {
		client = originalClient
		distinctId = originalDistinctId
	})

	mockClient := NewMockPosthogClient(gomock.NewController(t))
	client = mockClient
	distinctId = "test-distinct-id"
	return mockClient
}

// expectCapture registers an Enqueue expectation asserting the enqueued
// message is a posthog.Capture with the given event name.
func expectCapture(t *testing.T, mockClient *MockPosthogClient, wantEvent string, assertProps func(t *testing.T, props posthog.Properties)) {
	t.Helper()
	mockClient.EXPECT().Enqueue(gomock.Any()).DoAndReturn(func(msg posthog.Message) error {
		capture, ok := msg.(posthog.Capture)
		require.True(t, ok, "expected posthog.Capture, got %T", msg)
		require.Equal(t, wantEvent, capture.Event)
		if assertProps != nil {
			assertProps(t, capture.Properties)
		}
		return nil
	})
}

func TestNoExtraPropSenders(t *testing.T) {
	tests := []struct {
		name  string
		fn    func()
		event string
	}{
		{"AppInitialized", AppInitialized, "app initialized"},
		{"SessionCreated", SessionCreated, "session created"},
		{"SessionDeleted", SessionDeleted, "session deleted"},
		{"SessionSwitched", SessionSwitched, "session switched"},
		{"FilePickerOpened", FilePickerOpened, "filepicker opened"},
		{"StatsViewed", StatsViewed, "stats viewed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := withMockClient(t)
			expectCapture(t, mockClient, tt.event, nil)
			tt.fn()
		})
	}
}

func TestAppExited(t *testing.T) {
	mockClient := withMockClient(t)

	var props posthog.Properties
	expectCapture(t, mockClient, "app exited", func(t *testing.T, p posthog.Properties) {
		props = p
	})
	mockClient.EXPECT().Close().Return(nil)

	AppExited()

	require.Contains(t, props, "app duration pretty")
	require.Contains(t, props, "app duration in seconds")
}

func TestPropsPassThroughSenders(t *testing.T) {
	tests := []struct {
		name  string
		fn    func(...any)
		event string
	}{
		{"PromptSent", PromptSent, "prompt sent"},
		{"PromptResponded", PromptResponded, "prompt responded"},
		{"TokensUsed", TokensUsed, "tokens used"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := withMockClient(t)
			expectCapture(t, mockClient, tt.event, func(t *testing.T, p posthog.Properties) {
				require.Equal(t, "bar", p["foo"])
			})
			tt.fn("foo", "bar")
		})
	}
}

func TestJSONFlagSenders(t *testing.T) {
	tests := []struct {
		name  string
		fn    func(bool)
		event string
	}{
		{"SessionListed", SessionListed, "session listed"},
		{"SessionShown", SessionShown, "session shown"},
		{"SessionLastShown", SessionLastShown, "session last shown"},
		{"SessionDeletedCommand", SessionDeletedCommand, "session deleted"},
		{"SessionRenamed", SessionRenamed, "session renamed"},
	}

	for _, tt := range tests {
		for _, jsonVal := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/json=%v", tt.name, jsonVal), func(t *testing.T) {
				mockClient := withMockClient(t)
				expectCapture(t, mockClient, tt.event, func(t *testing.T, p posthog.Properties) {
					require.Equal(t, jsonVal, p["json"])
				})
				tt.fn(jsonVal)
			})
		}
	}
}

func TestSendNilClientIsNoop(t *testing.T) {
	originalClient := client
	defer func() { client = originalClient }()

	client = nil
	StatsViewed()
}

func TestAlias(t *testing.T) {
	tests := []struct {
		name          string
		clientNil     bool
		distinctId    string
		userID        string
		expectEnqueue bool
	}{
		{"client nil", true, "some-id", "user-1", false},
		{"distinctId empty", false, "", "user-1", false},
		{"distinctId fallback", false, fallbackId, "user-1", false},
		{"userID empty", false, "some-id", "", false},
		{"happy path", false, "some-id", "user-1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalClient := client
			originalDistinctId := distinctId
			defer func() {
				client = originalClient
				distinctId = originalDistinctId
			}()

			mockClient := NewMockPosthogClient(gomock.NewController(t))
			distinctId = tt.distinctId
			if tt.clientNil {
				client = nil
			} else {
				client = mockClient
			}

			if tt.expectEnqueue {
				mockClient.EXPECT().Enqueue(gomock.Any()).DoAndReturn(func(msg posthog.Message) error {
					alias, ok := msg.(posthog.Alias)
					require.True(t, ok, "expected posthog.Alias, got %T", msg)
					require.Equal(t, tt.distinctId, alias.DistinctId)
					require.Equal(t, tt.userID, alias.Alias)
					return nil
				})
			}

			Alias(tt.userID)
		})
	}
}

func TestAlias_LogsButDoesNotPanicWhenEnqueueFails(t *testing.T) {
	mockClient := withMockClient(t)
	mockClient.EXPECT().Enqueue(gomock.Any()).Return(errors.New("enqueue failed"))

	Alias("user-1")
}

func TestSend_LogsButDoesNotPanicWhenEnqueueFails(t *testing.T) {
	mockClient := withMockClient(t)
	mockClient.EXPECT().Enqueue(gomock.Any()).Return(errors.New("enqueue failed"))

	send("test event")
}

func TestFlush(t *testing.T) {
	t.Run("nil client is a no-op", func(t *testing.T) {
		originalClient := client
		defer func() { client = originalClient }()

		client = nil
		Flush()
	})

	t.Run("closes client without error", func(t *testing.T) {
		originalClient := client
		defer func() { client = originalClient }()

		mockClient := NewMockPosthogClient(gomock.NewController(t))
		mockClient.EXPECT().Close().Return(nil)
		client = mockClient

		Flush()
	})

	t.Run("logs but does not panic when close fails", func(t *testing.T) {
		originalClient := client
		defer func() { client = originalClient }()

		mockClient := NewMockPosthogClient(gomock.NewController(t))
		mockClient.EXPECT().Close().Return(errors.New("close failed"))
		client = mockClient

		Flush()
	})
}

func TestInit(t *testing.T) {
	originalClient := client
	originalDistinctId := distinctId
	defer func() {
		client = originalClient
		distinctId = originalDistinctId
	}()

	Init()
	defer Flush()

	require.NotNil(t, client)
	require.NotEmpty(t, GetID())
}

func TestHashString(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty string", "", "85c3300cfcca81d1e29ba9d44c9d2e2766a49a01f90dd7e88198263200086177"},
		{"simple string", "hello", "9f9db6676e970ca544c0af7e588a4f63ceb3bb9cb169f18828340c228570837f"},
		{"mac address format", "AA:BB:CC:DD:EE:FF", "0f444ea5af7b4a2f3073daaec474508a621c873f8dd3008aac70f0e081250dd3"},
		{"generic string", "some-mac-address", "fdd1a8bcb005ca6000b917be36028dd4cba698533994366ed75c4331b904a894"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, hashString(tt.in))
		})
	}
}

func TestGetDistinctId(t *testing.T) {
	require.NotEmpty(t, getDistinctId())
}

func TestGetMacAddr(t *testing.T) {
	addr, err := getMacAddr()
	if err != nil {
		// No active non-loopback interface with a MAC address on this host;
		// acceptable in sandboxed/CI network namespaces.
		return
	}
	require.NotEmpty(t, addr)
}

func TestLogger(t *testing.T) {
	originalLogger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(originalLogger) })

	tests := []struct {
		name  string
		call  func(logger, string, ...any)
		level string
	}{
		{"Debugf", logger.Debugf, "DEBUG"},
		{"Logf", logger.Logf, "INFO"},
		{"Warnf", logger.Warnf, "WARN"},
		{"Errorf", logger.Errorf, "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))

			tt.call(logger{}, "test message %d", 42)

			output := buf.String()
			require.Contains(t, output, "level="+tt.level)
			require.Contains(t, output, "test message 42")
		})
	}
}
