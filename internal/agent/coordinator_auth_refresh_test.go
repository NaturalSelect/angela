package agent

import (
	"context"
	"testing"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/oauth"
	"github.com/stretchr/testify/require"
)

// TestRetryAfterUnauthorizedWithNoRefreshMechanismIsANoOp pins the
// switch's default case: a provider with neither an OAuth token, an
// AWS refresh command, nor a resolvable API key template has nothing
// to retry with, so the 401 stands.
func TestRetryAfterUnauthorizedWithNoRefreshMechanismIsANoOp(t *testing.T) {
	t.Parallel()

	coord := newGateTestCoordinator(t, false)
	err := coord.retryAfterUnauthorized(t.Context(), config.ProviderConfig{ID: "mock"})
	require.NoError(t, err)
}

// TestRetryAfterUnauthorizedRoutesAWSProvidersToSSORefresh pins that
// the AWS branch of the switch is chosen purely from AWSAuthRefresh
// being set, and that it delegates to refreshAWSCredentials (whose own
// no-notify guard makes this deterministic and offline).
func TestRetryAfterUnauthorizedRoutesAWSProvidersToSSORefresh(t *testing.T) {
	t.Parallel()

	coord := newGateTestCoordinator(t, false)
	coord.notify = nil // refreshAWSCredentials fails fast without a notifier.

	err := coord.retryAfterUnauthorized(t.Context(), config.ProviderConfig{
		ID:             "mock",
		AWSAuthRefresh: "aws sso login",
	})
	require.ErrorIs(t, err, errNoInteractiveAuth)
}

// TestRetryAfterUnauthorizedRoutesTemplatedKeysToReresolution pins
// that the API-key-template branch is chosen purely from the template
// containing a "$", and that a resolution failure propagates.
func TestRetryAfterUnauthorizedRoutesTemplatedKeysToReresolution(t *testing.T) {
	t.Parallel()

	coord := newGateTestCoordinator(t, false)
	err := coord.retryAfterUnauthorized(t.Context(), config.ProviderConfig{
		ID:             "mock",
		APIKeyTemplate: "$", // A lone "$" is always a resolution error.
	})
	require.Error(t, err)
}

// TestRetryAfterUnauthorizedPropagatesNonRevokedOAuthErrors pins that
// an OAuth refresh failure which is not a revoked-refresh-token error
// is returned as-is, without opening the interactive re-auth dialog.
func TestRetryAfterUnauthorizedPropagatesNonRevokedOAuthErrors(t *testing.T) {
	t.Parallel()

	coord := newGateTestCoordinator(t, false)
	err := coord.retryAfterUnauthorized(t.Context(), config.ProviderConfig{
		ID:         "a-provider-with-no-config-entry",
		OAuthToken: &oauth.Token{},
	})
	require.ErrorContains(t, err, "not found")
}

// TestRefreshOAuth2TokenReturnsErrorForUnknownProvider pins that a
// provider missing from config surfaces the underlying store error
// rather than attempting a network exchange.
func TestRefreshOAuth2TokenReturnsErrorForUnknownProvider(t *testing.T) {
	t.Parallel()

	coord := newGateTestCoordinator(t, false)
	err := coord.refreshOAuth2Token(t.Context(), config.ProviderConfig{ID: "not-configured-provider"})
	require.ErrorContains(t, err, "not found")
}

// TestRefreshApiKeyTemplateResolvesAndStoresTheNewKey pins the happy
// path: a re-resolved template is written back onto the stored
// provider config, not just returned. t.Setenv rules out t.Parallel.
func TestRefreshApiKeyTemplateResolvesAndStoresTheNewKey(t *testing.T) {
	coord := newGateTestCoordinator(t, false)
	t.Setenv("ANGELA_TEST_REFRESH_API_KEY", "fresh-secret")

	err := coord.refreshApiKeyTemplate(t.Context(), config.ProviderConfig{
		ID:             "mock",
		APIKeyTemplate: "$ANGELA_TEST_REFRESH_API_KEY",
	})
	require.NoError(t, err)

	stored, ok := coord.cfg.Config().Providers.Get("mock")
	require.True(t, ok)
	require.Equal(t, "fresh-secret", stored.APIKey)
}

// TestRefreshApiKeyTemplateReturnsResolveError pins that a malformed
// template fails instead of silently storing an empty key.
func TestRefreshApiKeyTemplateReturnsResolveError(t *testing.T) {
	t.Parallel()

	coord := newGateTestCoordinator(t, false)
	err := coord.refreshApiKeyTemplate(t.Context(), config.ProviderConfig{
		ID:             "mock",
		APIKeyTemplate: "$",
	})
	require.Error(t, err)
}

// TestWaitForInteractiveReauthSucceedsWhenAlreadySignaled pins the
// generation-based rendezvous: a signal recorded before the wait
// starts still counts, so waitForInteractiveReauth returns
// immediately rather than blocking for a signal that already
// happened.
func TestWaitForInteractiveReauthSucceedsWhenAlreadySignaled(t *testing.T) {
	t.Parallel()

	coord := newGateTestCoordinator(t, false)
	since := coord.cfg.AuthGeneration("mock")
	coord.cfg.SignalAuthComplete("mock")

	err := coord.waitForInteractiveReauth(t.Context(), "mock", since)
	require.NoError(t, err)
}

// TestWaitForInteractiveReauthSurfacesCancellationAfterTheSignal pins
// that once the wait itself resolves, a turn whose own context was
// separately cancelled is reported as cancelled rather than retried
// with UpdateModels never having been reached.
func TestWaitForInteractiveReauthSurfacesCancellationAfterTheSignal(t *testing.T) {
	t.Parallel()

	coord := newGateTestCoordinator(t, false)
	since := coord.cfg.AuthGeneration("mock")
	coord.cfg.SignalAuthComplete("mock")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := coord.waitForInteractiveReauth(ctx, "mock", since)
	require.ErrorIs(t, err, context.Canceled)
}
