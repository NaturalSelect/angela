package common

import (
	"math"
	"time"

	"github.com/NaturalSelect/angela/internal/message"
)

// tpsMinDuration is the shortest step duration a generation rate is
// computed for. Below it, second-granularity Unix timestamps make the
// rate too noisy to be worth showing (a step measured at 0-1s could
// really be anywhere from a fraction of a second to just under two).
const tpsMinDuration = time.Second

// StepTPS reports the model's raw generation rate for a single agent
// step, rounded to the nearest token/sec, and whether the step is
// eligible to report one at all. See stepTPSInputs for the
// eligibility guard.
func StepTPS(msg *message.Message) (tps int64, ok bool) {
	duration, outputTokens, ok := stepTPSInputs(msg)
	if !ok {
		return 0, false
	}
	return int64(math.Round(float64(outputTokens) / duration.Seconds())), true
}

// StepTPSRate reports the same rate as StepTPS but unrounded, for
// callers aggregating many steps (min/median/p90/max) where rounding
// each step first would compound error.
func StepTPSRate(msg *message.Message) (tps float64, ok bool) {
	duration, outputTokens, ok := stepTPSInputs(msg)
	if !ok {
		return 0, false
	}
	return float64(outputTokens) / duration.Seconds(), true
}

// stepTPSInputs applies the shared TPS eligibility guard and returns
// the step's own duration (CreatedAt to Finish.Time) and output token
// count when it passes.
//
// NOTE: a step is excluded whenever it has any tool call. Permission
// waits, PreToolUse hooks, and tool execution all happen inside the
// same step's wall-clock window before OnStepFinish fires, so a step
// with a tool call would mix unbounded human/tool wait time into what
// is supposed to measure the model alone. It is also excluded when it
// produced no output tokens, or when its own duration is under a
// second.
func stepTPSInputs(msg *message.Message) (duration time.Duration, outputTokens int64, ok bool) {
	if msg == nil {
		return 0, 0, false
	}
	finish := msg.FinishPart()
	if finish == nil || finish.OutputTokens <= 0 || len(msg.ToolCalls()) != 0 {
		return 0, 0, false
	}
	duration = time.Unix(finish.Time, 0).Sub(time.Unix(msg.CreatedAt, 0))
	if duration < tpsMinDuration {
		return 0, 0, false
	}
	return duration, finish.OutputTokens, true
}
