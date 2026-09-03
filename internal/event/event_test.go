package event

// These tests verify that the Error function correctly handles various
// scenarios. These tests will not log anything.

import (
	"errors"
	"reflect"
	"testing"

	"github.com/posthog/posthog-go"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestSetNonInteractive(t *testing.T) {
	originalNonInteractive := baseProps[nonInteractiveAttrName]
	originalNonInteractiveNested := baseProps[nonInteractiveNestedAttrName]
	t.Cleanup(func() {
		baseProps = baseProps.
			Set(nonInteractiveAttrName, originalNonInteractive).
			Set(nonInteractiveNestedAttrName, originalNonInteractiveNested)
	})

	tests := []struct {
		name                     string
		nonInteractive           bool
		angela                   string
		wantNonInteractiveNested bool
	}{
		{
			name: "interactive direct invocation",
		},
		{
			name:           "non-interactive direct invocation",
			nonInteractive: true,
		},
		{
			name:   "interactive nested invocation",
			angela: "1",
		},
		{
			name:                     "non-interactive nested invocation",
			nonInteractive:           true,
			angela:                   "1",
			wantNonInteractiveNested: true,
		},
		{
			name:           "non-interactive invocation with unrecognized marker",
			nonInteractive: true,
			angela:         "0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ANGELA", tt.angela)
			SetNonInteractive(tt.nonInteractive)

			if got := baseProps[nonInteractiveAttrName]; got != tt.nonInteractive {
				t.Errorf("%s = %v, want %v", nonInteractiveAttrName, got, tt.nonInteractive)
			}
			if got := baseProps[nonInteractiveNestedAttrName]; got != tt.wantNonInteractiveNested {
				t.Errorf("%s = %v, want %v", nonInteractiveNestedAttrName, got, tt.wantNonInteractiveNested)
			}
		})
	}
}

func TestSetContinueBySessionID(t *testing.T) {
	original := baseProps[continueSessionByIDAttrName]
	t.Cleanup(func() {
		baseProps = baseProps.Set(continueSessionByIDAttrName, original)
	})

	SetContinueBySessionID(true)
	require.Equal(t, true, baseProps[continueSessionByIDAttrName])

	SetContinueBySessionID(false)
	require.Equal(t, false, baseProps[continueSessionByIDAttrName])
}

func TestSetContinueLastSession(t *testing.T) {
	original := baseProps[continueLastSessionAttrName]
	t.Cleanup(func() {
		baseProps = baseProps.Set(continueLastSessionAttrName, original)
	})

	SetContinueLastSession(true)
	require.Equal(t, true, baseProps[continueLastSessionAttrName])

	SetContinueLastSession(false)
	require.Equal(t, false, baseProps[continueLastSessionAttrName])
}

func TestError(t *testing.T) {
	t.Run("returns early when client is nil", func(t *testing.T) {
		// This test verifies that when the PostHog client is not initialized
		// the Error function safely returns early without attempting to
		// enqueue any events. This is important during initialization or when
		// metrics are disabled, as we don't want the error reporting mechanism
		// itself to cause panics.
		originalClient := client
		defer func() {
			client = originalClient
		}()

		client = nil
		Error("test error", "key", "value")
	})

	t.Run("handles nil client without panicking", func(t *testing.T) {
		// This test covers various edge cases where the error value might be
		// nil, a string, or an error type.
		originalClient := client
		defer func() {
			client = originalClient
		}()

		client = nil
		Error(nil)
		Error("some error")
		Error(newDefaultTestError("runtime error"), "key", "value")
	})

	t.Run("handles error with properties", func(t *testing.T) {
		// This test verifies that the Error function can handle additional
		// key-value properties that provide context about the error. These
		// properties are typically passed when recovering from panics (i.e.,
		// panic name, function name).
		//
		// Even with these additional properties, the function should handle
		// them gracefully without panicking.
		originalClient := client
		defer func() {
			client = originalClient
		}()

		client = nil
		Error(
			"test error",
			"type", "test",
			"severity", "high",
			"source", "unit-test",
		)
	})

	t.Run("enqueues an exception when client is set", func(t *testing.T) {
		originalClient := client
		originalDistinctId := distinctId
		defer func() {
			client = originalClient
			distinctId = originalDistinctId
		}()

		mockClient := NewMockPosthogClient(gomock.NewController(t))
		client = mockClient
		distinctId = "test-distinct-id"

		mockClient.EXPECT().Enqueue(gomock.Any()).DoAndReturn(func(msg posthog.Message) error {
			exception, ok := msg.(posthog.Exception)
			require.True(t, ok, "expected posthog.Exception, got %T", msg)
			require.Equal(t, distinctId, exception.DistinctId)
			require.Len(t, exception.ExceptionList, 1)
			require.Equal(t, "test error", exception.ExceptionList[0].Value)
			require.Equal(t, "bar", exception.Properties["foo"])
			return nil
		})

		Error(errors.New("test error"), "foo", "bar")
	})

	t.Run("logs but does not panic when enqueue fails", func(t *testing.T) {
		originalClient := client
		originalDistinctId := distinctId
		defer func() {
			client = originalClient
			distinctId = originalDistinctId
		}()

		mockClient := NewMockPosthogClient(gomock.NewController(t))
		client = mockClient
		distinctId = "test-distinct-id"

		mockClient.EXPECT().Enqueue(gomock.Any()).Return(errors.New("enqueue failed"))

		Error("boom")
	})
}

func TestPairsToProps(t *testing.T) {
	t.Run("sets valid key value pairs", func(t *testing.T) {
		got := pairsToProps("foo", "bar", "count", 3)
		want := posthog.NewProperties().
			Set("foo", "bar").
			Set("count", 3)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("pairsToProps() = %#v, want %#v", got, want)
		}
	})

	t.Run("returns empty properties for odd pairs", func(t *testing.T) {
		got := pairsToProps("foo", "bar", "count")
		if len(got) != 0 {
			t.Fatalf("pairsToProps() should return empty properties, got %#v", got)
		}
	})

	t.Run("ignores non-string key and continues", func(t *testing.T) {
		got := pairsToProps(123, "bad", "ok", true)
		want := posthog.NewProperties().Set("ok", true)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("pairsToProps() = %#v, want %#v", got, want)
		}
	})
}

// newDefaultTestError creates a test error that mimics runtime panic
// errors. This helps us testing that the Error function can handle various
// error types, including those that might be passed from a panic recovery
// scenario.
func newDefaultTestError(s string) error {
	return testError(s)
}

type testError string

func (e testError) Error() string {
	return string(e)
}
