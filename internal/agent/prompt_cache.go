package agent

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/bedrock"
	"charm.land/fantasy/providers/vercel"
	"github.com/NaturalSelect/angela/internal/session"
)

// cacheBreakpointIndices picks which messages in this step's prompt get
// an Anthropic-style cache breakpoint: the last system message, the tip
// of this request (its final message), and the tip of the previous
// completed turn.
//
// The previous turn's tip is the last User-role message before the last
// Assistant-role message that precedes this turn's tip, found by
// walking role transitions rather than a fixed offset from the end. A
// turn can end with more than one consecutive User-role message —
// reminders (dispatch nudges, todo recency, MCP status, etc.) are
// persisted immediately ahead of the prompt they belong to, and folded
// queued prompts land the same way — so a fixed "last N" offset can
// land inside the current turn's own tail instead of on the previous
// turn's boundary. That tail grows and changes every turn, so a
// breakpoint pinned to it would never match what was cached last time;
// walking to the role transition instead lands on the same message
// every time it is computed, because reminders and prompts are
// persisted once and never rewritten.
func cacheBreakpointIndices(messages []fantasy.Message) []int {
	var indices []int
	lastSystemRoleInx := -1
	for i, msg := range messages {
		if msg.Role == fantasy.MessageRoleSystem {
			lastSystemRoleInx = i
		}
	}
	if lastSystemRoleInx >= 0 {
		indices = append(indices, lastSystemRoleInx)
	}

	tip := len(messages) - 1
	if tip < 0 {
		return indices
	}
	indices = append(indices, tip)

	assistantIdx := -1
	for i := tip - 1; i >= 0; i-- {
		if messages[i].Role == fantasy.MessageRoleAssistant {
			assistantIdx = i
			break
		}
	}
	if assistantIdx < 0 {
		return indices
	}
	for i := assistantIdx - 1; i >= 0; i-- {
		if messages[i].Role == fantasy.MessageRoleUser {
			indices = append(indices, i)
			break
		}
	}
	return indices
}

func (a *sessionAgent) getCacheControlOptions() fantasy.ProviderOptions {
	if t, _ := strconv.ParseBool(os.Getenv("ANGELA_DISABLE_ANTHROPIC_CACHE")); t {
		return fantasy.ProviderOptions{}
	}
	return fantasy.ProviderOptions{
		anthropic.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
		bedrock.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
		vercel.Name: &anthropic.ProviderCacheControlOptions{
			CacheControl: anthropic.CacheControl{Type: "ephemeral"},
		},
	}
}

// sessionHeaders returns the HTTP headers we use for cache affinity on
// every LLM request for a given session.
//
// We use the session hash is used instead of the raw UUID so the header
// value is deterministic and opaque.
func sessionHeaders(sessionID string) map[string]string {
	hash := session.HashID(sessionID)
	return map[string]string{
		"x-session-id":       hash,
		"x-session-affinity": hash,
	}
}

func buildPromptCacheKey(sessionID, agentName string) string {
	return session.HashID(sessionID) + "-" + agentName
}

// buildAnthropicUserID derives a stable, non-identifying value for the
// Anthropic Messages API "metadata.user_id" field from promptCacheKey.
// This mirrors Claude Code's deriveClaudeCodeUserID scheme: three
// independent SHA-256 digests of promptCacheKey (two of them prefixed
// to keep the "account" and "session" components distinct) combined
// into user_<hash>_account_<uuid>_session_<uuid>. The "uuid" parts are
// not random; toUUID only reshapes a hash to look like a UUIDv4 so
// systems that validate the format accept it.
func buildAnthropicUserID(promptCacheKey string) string {
	hash := sha256Hex(promptCacheKey)
	accountHash := sha256Hex("account:" + promptCacheKey)
	sessionHash := sha256Hex("session:" + promptCacheKey)
	return "user_" + hash + "_account_" + toUUID(accountHash) + "_session_" + toUUID(sessionHash)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// toUUID reshapes a 64-character hex digest into a UUIDv4-shaped
// string (xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx). It is deterministic,
// not random.
func toUUID(h string) string {
	nibble, _ := strconv.ParseUint(h[16:17], 16, 8)
	variant := (nibble & 0x3) | 0x8
	return fmt.Sprintf("%s-%s-4%s-%x%s-%s", h[0:8], h[8:12], h[13:16], variant, h[17:20], h[20:32])
}
