package config

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/env"
	"github.com/NaturalSelect/angela/internal/lock"
	"github.com/NaturalSelect/angela/internal/oauth"
	"github.com/NaturalSelect/angela/internal/oauth/copilot"
	"github.com/tidwall/gjson"
)

// RefreshOAuthToken refreshes the OAuth token for the given provider.
//
// Some providers rotate refresh tokens: each exchange consumes the
// caller's refresh token, issues a new pair, and revokes the old one. If
// two angela instances (or two goroutines) refresh concurrently with the
// same stored refresh token, the second exchange reuses an already-revoked
// token, trips the provider's reuse detection, and revokes the entire
// token family — leaving both with dead tokens even though each refresh
// "succeeded".
//
// To prevent that, refreshes are single-flighted at two levels:
//
//   - In-process: refreshSF collapses concurrent goroutines for the same
//     provider into one attempt.
//   - Cross-process: a per-provider advisory lock is held across the whole
//     read-decide-exchange-write cycle, so only one process exchanges at a
//     time. A process that acquires the lock after a peer rotated finds the
//     peer's fresh token on disk and adopts it instead of exchanging.
func (s *ConfigStore) RefreshOAuthToken(ctx context.Context, scope Scope, providerID string) error {
	key := fmt.Sprintf("%d\x00%s", scope, providerID)
	_, err, _ := s.refreshSF.Do(key, func() (any, error) {
		return nil, s.refreshOAuthTokenLocked(ctx, scope, providerID)
	})
	return err
}

// refreshOAuthTokenLocked performs the cross-process single-flighted
// refresh. It is invoked through refreshSF, so at most one goroutine per
// provider runs it at a time within this process.
func (s *ConfigStore) refreshOAuthTokenLocked(ctx context.Context, scope Scope, providerID string) error {
	cfg := s.Config()
	providerConfig, exists := cfg.Providers.Get(providerID)
	if !exists {
		return fmt.Errorf("provider %s not found", providerID)
	}
	if providerConfig.OAuthToken == nil {
		return fmt.Errorf("provider %s does not have an OAuth token", providerID)
	}
	entryToken := providerConfig.OAuthToken

	// Acquire the per-provider cross-process refresh lock. This is a
	// dedicated lock file, not the config-write lock, and it does not take
	// s.mu — so the network exchange below cannot stall unrelated config
	// operations. The deadline exceeds the exchange timeout so that a peer
	// mid-exchange has time to publish a token we can adopt. Lock ordering:
	// the refresh lock is always taken before the config-write lock (via
	// SetConfigFields), never the reverse, so no deadlock is possible.
	lockCtx, cancel := context.WithTimeout(ctx, refreshLockDeadline)
	defer cancel()
	release, lockErr := lock.File(lockCtx, s.refreshLockPath(providerID))
	if lockErr != nil {
		// Could not acquire the lock (peer wedged or deadline hit). Prefer a
		// usable token already on disk over forcing our own exchange, which
		// would risk reusing a rotated refresh token.
		if diskToken := s.usableDiskToken(scope, providerID, entryToken); diskToken != nil {
			slog.Warn("Refresh lock unavailable; adopting token from disk", "provider", providerID, "error", lockErr)
			return s.applyToken(providerConfig, diskToken, providerID)
		}
		return fmt.Errorf("acquire refresh lock for provider %s: %w", providerID, lockErr)
	}
	defer release()

	// Now that we hold the lock, disk is the authority on which credential
	// is current: a peer may have rotated ours away while we waited. Adopt
	// a newer token outright when it is still usable, and otherwise switch
	// to its refresh token for the exchange below. Presenting our own
	// already-rotated refresh token would trip the provider's reuse
	// detection and revoke the whole family, forcing an interactive login.
	if diskToken := s.newerDiskToken(scope, providerID, entryToken); diskToken != nil {
		if !diskToken.IsExpired() {
			slog.Info("Adopting token refreshed by another session", "provider", providerID)
			return s.applyToken(providerConfig, diskToken, providerID)
		}
		slog.Info("Exchanging with refresh token rotated by another session", "provider", providerID)
		entryToken = diskToken
	}

	// Disk still holds our token (or no newer peer token exists) and we hold
	// the lock, so we are the sole exchanger. Perform the exchange.
	refreshedToken, refreshErr := s.exchange(ctx, providerID, entryToken.RefreshToken)
	if refreshErr != nil {
		// The exchange may have failed because a peer rotated the refresh
		// token in a window we did not cover. Re-check disk: adopt a usable
		// token, or retry once with the peer's newer refresh token.
		if diskToken := s.newerDiskToken(scope, providerID, entryToken); diskToken != nil {
			if !diskToken.IsExpired() {
				slog.Info("Adopting token refreshed by another session after exchange failure", "provider", providerID)
				return s.applyToken(providerConfig, diskToken, providerID)
			}
			slog.Info("Retrying exchange with refresh token rotated by another session", "provider", providerID)
			refreshedToken, refreshErr = s.exchange(ctx, providerID, diskToken.RefreshToken)
		}
	}
	if refreshErr != nil {
		return fmt.Errorf("failed to refresh OAuth token for provider %s: %w", providerID, refreshErr)
	}

	slog.Info("Successfully refreshed OAuth token", "provider", providerID)
	if err := s.applyToken(providerConfig, refreshedToken, providerID); err != nil {
		return err
	}

	if err := s.SetConfigFields(scope, map[string]any{
		fmt.Sprintf("providers.%s.api_key", providerID): refreshedToken.AccessToken,
		fmt.Sprintf("providers.%s.oauth", providerID):   refreshedToken,
	}); err != nil {
		return fmt.Errorf("failed to persist refreshed token: %w", err)
	}
	return nil
}

// newerDiskToken returns the on-disk token for the provider when it is
// newer than entryToken — i.e. another session (possibly in another
// process) has already rotated the credential. It returns nil when disk
// holds nothing newer than what we started with.
//
// Newness is judged by expiry as well as identity, so a config file that
// somehow holds an older token cannot drag us backwards. The result may
// itself be expired: providers that rotate refresh tokens invalidate ours
// the moment a peer refreshes, so the peer's refresh token is the only one
// the provider will still accept even after its access token ages out.
// Callers decide whether to adopt the token wholesale or merely borrow its
// refresh token.
func (s *ConfigStore) newerDiskToken(scope Scope, providerID string, entryToken *oauth.Token) *oauth.Token {
	diskToken, err := s.loadTokenFromDisk(scope, providerID)
	if err != nil {
		slog.Warn("Failed to read token from config file", "provider", providerID, "error", err)
		return nil
	}
	if diskToken == nil {
		return nil
	}
	if diskToken.AccessToken == entryToken.AccessToken {
		// Same token we started with; nobody rotated since.
		return nil
	}
	if diskToken.RefreshToken == "" && entryToken.RefreshToken != "" {
		// Adopting would strand us with no way to refresh later, and
		// there is nothing to borrow for an exchange.
		return nil
	}
	if diskToken.ExpiresAt < entryToken.ExpiresAt {
		// Older than ours; nothing to gain from adopting it.
		return nil
	}
	return diskToken
}

// usableDiskToken returns the on-disk token only when it is both newer
// than entryToken and still valid, meaning it can be adopted as-is with
// no exchange at all.
func (s *ConfigStore) usableDiskToken(scope Scope, providerID string, entryToken *oauth.Token) *oauth.Token {
	diskToken := s.newerDiskToken(scope, providerID, entryToken)
	if diskToken == nil || diskToken.IsExpired() {
		return nil
	}
	return diskToken
}

// exchange performs the provider-specific OAuth token exchange. Tests may
// override it via the exchangeToken field; production uses the real
// provider clients.
func (s *ConfigStore) exchange(ctx context.Context, providerID, refreshToken string) (*oauth.Token, error) {
	if s.exchangeToken != nil {
		return s.exchangeToken(ctx, providerID, refreshToken)
	}
	switch providerID {
	case string(catwalk.InferenceProviderCopilot):
		return copilot.RefreshToken(ctx, refreshToken)
	default:
		return nil, fmt.Errorf("OAuth refresh not supported for provider %s", providerID)
	}
}

// withRefreshLock runs fn while holding the per-provider cross-process
// refresh lock, so a credential write cannot interleave with a peer's
// token exchange. Acquisition is best effort: when the lock cannot be
// taken in time, fn runs anyway rather than blocking a write the user is
// waiting on.
func (s *ConfigStore) withRefreshLock(providerID string, fn func() error) error {
	ctx, cancel := context.WithTimeout(context.Background(), credentialWriteLockDeadline)
	defer cancel()
	release, err := lock.File(ctx, s.refreshLockPath(providerID))
	if err != nil {
		slog.Warn("Writing credentials without the refresh lock", "provider", providerID, "error", err)
		return fn()
	}
	defer release()
	return fn()
}

// refreshLockPath returns the path to the per-provider cross-process refresh
// lock file. Lock files live under a dedicated locks/ subdirectory of the
// data dir so they do not clutter the config directory. The file is created
// on demand by lock.File and is never removed (flock keys on inode, not
// path).
func (s *ConfigStore) refreshLockPath(providerID string) string {
	dir := filepath.Join(filepath.Dir(s.globalDataPath), "locks")
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, fmt.Sprintf("%s.refresh.lock", providerID))
}

// applyToken updates the in-memory provider config with the given token.
func (s *ConfigStore) applyToken(providerConfig ProviderConfig, token *oauth.Token, providerID string) error {
	providerConfig.OAuthToken = token
	providerConfig.APIKey = token.AccessToken
	if providerID == string(catwalk.InferenceProviderCopilot) {
		providerConfig.SetupGitHubCopilot()
	}
	s.Config().Providers.Set(providerID, providerConfig)
	return nil
}

// loadTokenFromDisk reads the OAuth token for the given provider from the
// config file on disk. Returns nil if the token is not found or matches the
// current in-memory token.
func (s *ConfigStore) loadTokenFromDisk(scope Scope, providerID string) (*oauth.Token, error) {
	path, err := s.configPath(scope)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	oauthKey := fmt.Sprintf("providers.%s.oauth", providerID)
	oauthResult := gjson.Get(string(data), oauthKey)
	if !oauthResult.Exists() {
		return nil, nil
	}

	var token oauth.Token
	if err := json.Unmarshal([]byte(oauthResult.Raw), &token); err != nil {
		return nil, err
	}

	if token.AccessToken == "" {
		return nil, nil
	}

	return &token, nil
}

// NewTestStore creates a ConfigStore for testing purposes.
func NewTestStore(cfg *Config, loadedPaths ...string) *ConfigStore {
	return &ConfigStore{
		config:      cfg,
		loadedPaths: loadedPaths,
		resolver:    NewShellVariableResolver(env.New()),
	}
}

// ImportCopilot attempts to import a GitHub Copilot token from disk.
func (s *ConfigStore) ImportCopilot() (*oauth.Token, bool) {
	if s.HasConfigField(ScopeGlobal, "providers.copilot.api_key") || s.HasConfigField(ScopeGlobal, "providers.copilot.oauth") {
		return nil, false
	}

	diskToken, hasDiskToken := copilot.RefreshTokenFromDisk()
	if !hasDiskToken {
		return nil, false
	}

	slog.Info("Found existing GitHub Copilot token on disk. Authenticating...")
	token, err := copilot.RefreshToken(context.TODO(), diskToken)
	if err != nil {
		slog.Error("Unable to import GitHub Copilot token", "error", err)
		return nil, false
	}

	if err := s.SetProviderAPIKey(ScopeGlobal, string(catwalk.InferenceProviderCopilot), token); err != nil {
		return token, false
	}

	if err := s.SetConfigFields(ScopeGlobal, map[string]any{
		"providers.copilot.api_key": token.AccessToken,
		"providers.copilot.oauth":   token,
	}); err != nil {
		slog.Error("Unable to save GitHub Copilot token to disk", "error", err)
	}

	slog.Info("GitHub Copilot successfully imported")
	return token, true
}
