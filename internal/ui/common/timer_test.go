package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFormatDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{name: "seconds only", d: 42 * time.Second, want: "42s"},
		{name: "zero duration", d: 0, want: "0s"},
		{name: "minutes and seconds", d: 90 * time.Second, want: "1m 30s"},
		{name: "an exact minute", d: 2 * time.Minute, want: "2m 0s"},
		{name: "hours and minutes", d: 90 * time.Minute, want: "1h 30m"},
		{name: "hours drop the seconds unit", d: time.Hour + 45*time.Second, want: "1h 0m"},
		{name: "minutes wrap with modulo past an hour", d: 125 * time.Minute, want: "2h 5m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, FormatDuration(tt.d))
		})
	}
}

// TestTurnTimer_StartStopElapsed is not parallel: StartTurn/StopTurn/Elapsed
// share package-level mutable state, so interleaving with another
// invocation of the same trio would make the sequence non-deterministic.
func TestTurnTimer_StartStopElapsed(t *testing.T) {
	require.Empty(t, Elapsed(), "no active turn before StartTurn")

	StartTurn()
	elapsed := Elapsed()
	require.NotEmpty(t, elapsed)
	require.Regexp(t, `^\d+s$`, elapsed)

	StopTurn()
	require.Empty(t, Elapsed(), "no active turn after StopTurn")
}
