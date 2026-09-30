package websearch

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// TimeRange narrows a search to results from a recent window (Days) or
// after an absolute date (After). ParseTimeRange sets exactly one.
type TimeRange struct {
	Days  float64
	After time.Time
}

// String renders the range for diagnostic messages (e.g. a Router
// fallback Note).
func (t TimeRange) String() string {
	if !t.After.IsZero() {
		return t.After.Format("2006-01-02")
	}
	return fmt.Sprintf("%gd", t.Days)
}

var namedRangeDays = map[string]float64{
	"day":   1,
	"week":  7,
	"month": 30,
	"year":  365,
}

var (
	relativeRangeRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(h|hour|hours|d|day|days|w|week|weeks|mo|month|months|y|year|years)$`)
	absoluteDateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// ParseTimeRange parses the model-facing time_range argument: a named
// range (day/week/month/year), a relative offset (12h, 3d, 2w, 2mo,
// 1y), or an absolute date (YYYY-MM-DD). An empty or all-whitespace
// string returns (nil, nil) — no filter requested.
func ParseTimeRange(s string) (*TimeRange, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return nil, nil
	}
	if days, ok := namedRangeDays[s]; ok {
		return &TimeRange{Days: days}, nil
	}
	if absoluteDateRe.MatchString(s) {
		after, err := time.Parse("2006-01-02", s)
		if err != nil {
			return nil, fmt.Errorf("websearch: invalid time_range date %q: %w", s, err)
		}
		return &TimeRange{After: after}, nil
	}
	if m := relativeRangeRe.FindStringSubmatch(s); m != nil {
		n, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return nil, fmt.Errorf("websearch: invalid time_range %q: %w", s, err)
		}
		var days float64
		switch m[2][0] {
		case 'h':
			days = n / 24
		case 'd':
			days = n
		case 'w':
			days = n * 7
		case 'm':
			days = n * 30
		case 'y':
			days = n * 365
		}
		return &TimeRange{Days: days}, nil
	}
	return nil, fmt.Errorf("websearch: unrecognized time_range %q: expected day/week/month/year, a relative offset like 3d or 2w, or an absolute date YYYY-MM-DD", s)
}

// ApproximateTier maps a custom day count onto the smallest of the fixed
// day/week/month/year buckets that fully covers it, for engines that only
// support named ranges (e.g. Tavily, SearXNG, DDG). Rounding up means a
// tier may return older results than asked for but never drops results
// inside the requested window.
func ApproximateTier(days float64) string {
	switch {
	case days <= namedRangeDays["day"]:
		return "day"
	case days <= namedRangeDays["week"]:
		return "week"
	case days <= namedRangeDays["month"]:
		return "month"
	default:
		return "year"
	}
}
