package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"charm.land/fantasy"
	"github.com/stretchr/testify/require"
)

// TestCompactAuthRefreshRebuilding covers compactAuthRefreshRebuilding
// (internal/agent/agent.go), the callback fantasy invokes on a 401 mid
// -summarize. It is otherwise only exercised indirectly through a live
// provider, so its branches (nil callback, refresh failure, missing
// rebuilder, rebuild failure, successful swap) had no direct coverage.
func TestCompactAuthRefreshRebuilding(t *testing.T) {
	t.Parallel()

	refreshErr := errors.New("refresh failed")
	rebuildErr := errors.New("rebuild failed")

	t.Run("nil callback means nothing to wrap", func(t *testing.T) {
		t.Parallel()

		got := compactAuthRefreshRebuilding("session", resolvedAgent{}, nil, &atomic.Pointer[fantasy.LanguageModel]{})
		require.Nil(t, got, "fantasy must see a nil OnAuthRefresh, not a no-op wrapper, when the caller supplied none")
	})

	t.Run("a failed refresh propagates and never rebuilds", func(t *testing.T) {
		t.Parallel()

		rebuildCalled := false
		compact := resolvedAgent{
			RebuildModel: func(context.Context) (fantasy.LanguageModel, error) {
				rebuildCalled = true
				return newMockLanguageModel(t), nil
			},
		}
		wrapped := compactAuthRefreshRebuilding("session", compact,
			func(context.Context, *fantasy.ProviderError) error { return refreshErr },
			&atomic.Pointer[fantasy.LanguageModel]{})

		require.ErrorIs(t, wrapped(t.Context(), nil), refreshErr)
		require.False(t, rebuildCalled, "a credential refresh that failed must not attempt to rebuild the model")
	})

	t.Run("no rebuilder leaves the retry model untouched", func(t *testing.T) {
		t.Parallel()

		original := fantasy.LanguageModel(newMockLanguageModel(t))
		retryModel := &atomic.Pointer[fantasy.LanguageModel]{}
		retryModel.Store(&original)

		compact := resolvedAgent{RebuildModel: nil}
		wrapped := compactAuthRefreshRebuilding("session", compact,
			func(context.Context, *fantasy.ProviderError) error { return nil },
			retryModel)

		require.NoError(t, wrapped(t.Context(), nil))
		require.Same(t, original, *retryModel.Load(),
			"without a rebuilder the previously resolved model must keep serving retries")
	})

	t.Run("a rebuild failure propagates and does not swap the retry model", func(t *testing.T) {
		t.Parallel()

		original := fantasy.LanguageModel(newMockLanguageModel(t))
		retryModel := &atomic.Pointer[fantasy.LanguageModel]{}
		retryModel.Store(&original)

		compact := resolvedAgent{
			RebuildModel: func(context.Context) (fantasy.LanguageModel, error) {
				return nil, rebuildErr
			},
		}
		wrapped := compactAuthRefreshRebuilding("session", compact,
			func(context.Context, *fantasy.ProviderError) error { return nil },
			retryModel)

		require.ErrorIs(t, wrapped(t.Context(), nil), rebuildErr)
		require.Same(t, original, *retryModel.Load(),
			"a failed rebuild must leave the stale-but-known model in place rather than clearing it")
	})

	t.Run("a successful rebuild swaps the retry model", func(t *testing.T) {
		t.Parallel()

		original := fantasy.LanguageModel(newMockLanguageModel(t))
		rebuilt := fantasy.LanguageModel(newMockLanguageModel(t))
		retryModel := &atomic.Pointer[fantasy.LanguageModel]{}
		retryModel.Store(&original)

		compact := resolvedAgent{
			RebuildModel: func(context.Context) (fantasy.LanguageModel, error) {
				return rebuilt, nil
			},
		}
		wrapped := compactAuthRefreshRebuilding("session", compact,
			func(context.Context, *fantasy.ProviderError) error { return nil },
			retryModel)

		require.NoError(t, wrapped(t.Context(), nil))
		require.Same(t, rebuilt, *retryModel.Load(),
			"fantasy must retry against the freshly rebuilt model, which holds the refreshed credentials")
	})
}
