package websearch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseTimeRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    *TimeRange
		wantErr bool
	}{
		{name: "empty", input: "", want: nil},
		{name: "whitespace only", input: "   ", want: nil},
		{name: "day", input: "day", want: &TimeRange{Days: 1}},
		{name: "week", input: "week", want: &TimeRange{Days: 7}},
		{name: "month", input: "month", want: &TimeRange{Days: 30}},
		{name: "year", input: "year", want: &TimeRange{Days: 365}},
		{name: "case insensitive", input: "DAY", want: &TimeRange{Days: 1}},
		{name: "relative hours", input: "12h", want: &TimeRange{Days: 0.5}},
		{name: "relative days", input: "3d", want: &TimeRange{Days: 3}},
		{name: "relative weeks", input: "2w", want: &TimeRange{Days: 14}},
		{name: "relative months", input: "2mo", want: &TimeRange{Days: 60}},
		{name: "relative years", input: "1y", want: &TimeRange{Days: 365}},
		{name: "relative spelled out", input: "3 days", want: &TimeRange{Days: 3}},
		{name: "absolute date", input: "2026-07-01", want: &TimeRange{After: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}},
		{name: "invalid unit", input: "3x", wantErr: true},
		{name: "garbage", input: "not a range", wantErr: true},
		{name: "malformed date", input: "2026-13-40", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseTimeRange(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestApproximateTier(t *testing.T) {
	t.Parallel()
	require.Equal(t, "day", ApproximateTier(1))
	require.Equal(t, "day", ApproximateTier(2))
	require.Equal(t, "week", ApproximateTier(3))
	require.Equal(t, "week", ApproximateTier(14))
	require.Equal(t, "month", ApproximateTier(15))
	require.Equal(t, "month", ApproximateTier(90))
	require.Equal(t, "year", ApproximateTier(91))
}
