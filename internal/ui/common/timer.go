package common

import (
	"fmt"
	"sync"
	"time"
)

// turnTimer tracks the elapsed time for the current agent turn.
var turnTimer struct {
	mu        sync.Mutex
	startTime time.Time
	active    bool
}

// StartTurn begins tracking elapsed time for a new turn.
func StartTurn() {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	turnTimer.startTime = time.Now()
	turnTimer.active = true
}

// StopTurn stops tracking the current turn.
func StopTurn() {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	turnTimer.active = false
}

// Elapsed returns the formatted elapsed time for the current turn.
// Returns empty string if no turn is active.
func Elapsed() string {
	turnTimer.mu.Lock()
	defer turnTimer.mu.Unlock()
	if !turnTimer.active {
		return ""
	}
	return FormatDuration(time.Since(turnTimer.startTime))
}

// FormatDuration renders a duration for one-line chrome, dropping the
// units too small to read at that magnitude.
func FormatDuration(d time.Duration) string {
	totalSeconds := int(d.Seconds())
	minutes := int(d.Minutes())
	hours := int(d.Hours())

	switch {
	case hours >= 1:
		return fmt.Sprintf("%dh %dm", hours, minutes%60)
	case minutes >= 1:
		return fmt.Sprintf("%dm %ds", minutes, totalSeconds%60)
	default:
		return fmt.Sprintf("%ds", totalSeconds)
	}
}
