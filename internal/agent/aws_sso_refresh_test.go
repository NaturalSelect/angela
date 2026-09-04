package agent

import (
	"context"
	"testing"
	"time"

	"github.com/NaturalSelect/angela/internal/agent/notify"
	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/stretchr/testify/require"
)

func TestExtractAWSSSOURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output string
		want   string
	}{
		{
			name: "standard aws sso login output",
			output: `If the browser does not open or you wish to use a different device to authorize this request, open the following URL:
https://device.sso.us-east-1.amazonaws.com/?user_code=ABCD-EFGH`,
			want: "https://device.sso.us-east-1.amazonaws.com/?user_code=ABCD-EFGH",
		},
		{
			name:   "url only",
			output: "https://device.sso.eu-west-1.amazonaws.com/?user_code=XXXX-YYYY",
			want:   "https://device.sso.eu-west-1.amazonaws.com/?user_code=XXXX-YYYY",
		},
		{
			name:   "no url",
			output: "SSO session expired. Please run aws sso login.",
			want:   "",
		},
		{
			name:   "empty output",
			output: "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := extractAWSSSOURL(tt.output); got != tt.want {
				t.Errorf("extractAWSSSOURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// recvNotification reads the next notification off the channel,
// failing the test if none arrives promptly. All of
// refreshAWSCredentials' publishes happen synchronously before it
// returns, so a short bound is enough.
func recvNotification(t *testing.T, ch <-chan pubsub.Event[notify.Notification]) notify.Notification {
	t.Helper()
	select {
	case ev := <-ch:
		return ev.Payload
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for notification")
		return notify.Notification{}
	}
}

// TestRefreshAWSCredentialsRequiresInteractiveNotify pins that a
// coordinator with no notify publisher refuses to run the refresh
// command at all — there would be nowhere to show the SSO prompt.
func TestRefreshAWSCredentialsRequiresInteractiveNotify(t *testing.T) {
	t.Parallel()

	c := &coordinator{}
	err := c.refreshAWSCredentials(t.Context(), config.ProviderConfig{ID: "bedrock"})
	require.ErrorIs(t, err, errNoInteractiveAuth)
}

// TestRefreshAWSCredentialsSucceeds pins the happy path end to end:
// the coordinator opens the SSO dialog, runs the command, picks up
// the verification URL from its output, and reports success.
func TestRefreshAWSCredentialsSucceeds(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := newTestCoordinator(t, env, "bedrock", config.ProviderConfig{ID: "bedrock"})
	broker := pubsub.NewBroker[notify.Notification]()
	coord.notify = broker

	subCtx, subCancel := context.WithCancel(t.Context())
	defer subCancel()
	events := broker.Subscribe(subCtx)

	providerCfg := config.ProviderConfig{
		ID:             "bedrock",
		AWSAuthRefresh: "echo 'open https://device.sso.example.com/?user_code=ABCD'",
	}
	err := coord.refreshAWSCredentials(t.Context(), providerCfg)
	require.NoError(t, err)

	opened := recvNotification(t, events)
	require.Equal(t, notify.TypeAWSSSOAuth, opened.Type)
	require.Empty(t, opened.AWSSOURL)

	withURL := recvNotification(t, events)
	require.Equal(t, notify.TypeAWSSSOAuth, withURL.Type)
	require.Equal(t, "https://device.sso.example.com/?user_code=ABCD", withURL.AWSSOURL)

	result := recvNotification(t, events)
	require.Equal(t, notify.TypeAWSSSOAuthResult, result.Type)
	require.Empty(t, result.Message)
}

// TestRefreshAWSCredentialsReturnsCommandFailure pins that a failing
// refresh command's error (with captured stderr) is both returned to
// the caller and published as the result's message.
func TestRefreshAWSCredentialsReturnsCommandFailure(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := newTestCoordinator(t, env, "bedrock", config.ProviderConfig{ID: "bedrock"})
	broker := pubsub.NewBroker[notify.Notification]()
	coord.notify = broker

	subCtx, subCancel := context.WithCancel(t.Context())
	defer subCancel()
	events := broker.Subscribe(subCtx)

	providerCfg := config.ProviderConfig{
		ID:             "bedrock",
		AWSAuthRefresh: "echo 'sso session expired' 1>&2; exit 7",
	}
	err := coord.refreshAWSCredentials(t.Context(), providerCfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "sso session expired")

	recvNotification(t, events) // Discard the opening TypeAWSSSOAuth.
	result := recvNotification(t, events)
	require.Equal(t, notify.TypeAWSSSOAuthResult, result.Type)
	require.Equal(t, err.Error(), result.Message)
}

// TestRefreshAWSCredentialsSurfacesCancellationAfterTheCommandRuns
// pins that once the refresh command finishes, a turn cancelled while
// it ran is reported as cancelled rather than silently retried, even
// though the command itself succeeded.
func TestRefreshAWSCredentialsSurfacesCancellationAfterTheCommandRuns(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := newTestCoordinator(t, env, "bedrock", config.ProviderConfig{ID: "bedrock"})
	broker := pubsub.NewBroker[notify.Notification]()
	coord.notify = broker

	subCtx, subCancel := context.WithCancel(t.Context())
	defer subCancel()
	events := broker.Subscribe(subCtx)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Already cancelled before the refresh starts.

	providerCfg := config.ProviderConfig{ID: "bedrock", AWSAuthRefresh: "true"}
	err := coord.refreshAWSCredentials(ctx, providerCfg)
	require.ErrorIs(t, err, context.Canceled)

	recvNotification(t, events) // Discard the opening TypeAWSSSOAuth.
	result := recvNotification(t, events)
	require.Equal(t, notify.TypeAWSSSOAuthResult, result.Type)
	require.Empty(t, result.Message, "the command itself succeeded; only the turn was cancelled")
}

// TestRunAWSAuthRefreshFindsURLOnStderr pins that the verification URL
// is picked up even when the command writes it to stderr instead of
// stdout — some AWS CLI versions do.
func TestRunAWSAuthRefreshFindsURLOnStderr(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := newTestCoordinator(t, env, "bedrock", config.ProviderConfig{ID: "bedrock"})
	broker := pubsub.NewBroker[notify.Notification]()
	coord.notify = broker

	subCtx, subCancel := context.WithCancel(t.Context())
	defer subCancel()
	events := broker.Subscribe(subCtx)

	providerCfg := config.ProviderConfig{
		ID:             "bedrock",
		AWSAuthRefresh: "echo 'open https://device.sso.example.com/?user_code=WXYZ' 1>&2",
	}
	err := coord.runAWSAuthRefresh(t.Context(), providerCfg)
	require.NoError(t, err)

	withURL := recvNotification(t, events)
	require.Equal(t, notify.TypeAWSSSOAuth, withURL.Type)
	require.Equal(t, "https://device.sso.example.com/?user_code=WXYZ", withURL.AWSSOURL)
}

// TestRunAWSAuthRefreshReturnsPlainErrorWithoutStderr pins that a
// failing command with no stderr output still returns a usable error
// (the exit status alone) rather than an empty message.
func TestRunAWSAuthRefreshReturnsPlainErrorWithoutStderr(t *testing.T) {
	t.Parallel()

	env := testEnv(t)
	coord := newTestCoordinator(t, env, "bedrock", config.ProviderConfig{ID: "bedrock"})

	providerCfg := config.ProviderConfig{ID: "bedrock", AWSAuthRefresh: "exit 3"}
	err := coord.runAWSAuthRefresh(t.Context(), providerCfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exit status 3")
}
