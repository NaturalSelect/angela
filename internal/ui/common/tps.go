package common

import (
	"math"
	"time"

	"github.com/NaturalSelect/angela/internal/message"
)

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

// AverageTPS reports the average generation rate across an accumulated
// total of output tokens and generation time, rounded to the nearest
// token/sec. It is used for a session-wide average (see
// session.Session.GenOutputTokens / GenDurationMs), as opposed to
// StepTPS/StepTPSRate which report a single step's rate.
func AverageTPS(outputTokens, durationMs int64) (tps int64, ok bool) {
	if outputTokens <= 0 || durationMs <= 0 {
		return 0, false
	}
	return int64(math.Round(float64(outputTokens) / (float64(durationMs) / 1000))), true
}

// stepTPSInputs applies the shared TPS eligibility guard and returns
// the step's generation duration and output token count when it
// passes.
//
// The duration comes from Finish.GenDurationMs, which the agent layer
// sets to the precise, millisecond-granularity wall-clock time of the
// model's own stream (request sent to stream finished). That duration
// already excludes tool execution, permission waits, and PreToolUse
// hooks, all of which happen later in the same step before
// OnStepFinish fires. Because of that, a step with tool calls no
// longer needs to be excluded here the way it once was: its
// GenDurationMs only ever covers the model's own generation, not the
// tool call that followed it.
//
// A step is excluded when it produced no output tokens, or when
// GenDurationMs is unset (0), which happens for older messages,
// cancellations, errors, or summary messages that never went through
// OnStepFinish.
func stepTPSInputs(msg *message.Message) (duration time.Duration, outputTokens int64, ok bool) {
	if msg == nil {
		return 0, 0, false
	}
	finish := msg.FinishPart()
	if finish == nil || finish.OutputTokens <= 0 || finish.GenDurationMs <= 0 {
		return 0, 0, false
	}
	return time.Duration(finish.GenDurationMs) * time.Millisecond, finish.OutputTokens, true
}
