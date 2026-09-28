package websearch

import (
	"regexp"
	"strings"
)

var snippetNoiseRe = regexp.MustCompile(`(?i)\b(sign up|sign in|log in|login|subscribe( to| for)?|member[- ]?only|become a member|create (a )?free account|read more|continue reading|story continues|get started|install (the )?app|view on|medium membership|join \w+ for free|get updates from this writer|stories in your inbox|remember me for|unlock this|free to read|become a patron)\b`)

var leadingMarkupRe = regexp.MustCompile(`(?m)^\s*(#{1,6}\s*|\[\s*x?\s*\]\s*|-\s*\[\s*x?\s*\]\s*|>\s*)`)

var whitespaceRunRe = regexp.MustCompile(`\s+`)

// snippetMaxRunes bounds a cleaned snippet's length.
const snippetMaxRunes = 300

// cleanSnippet strips common login/paywall/subscription noise phrases
// and markdown-list/heading leftovers, collapses whitespace, and
// truncates to a bounded length. Applied once at the Router's exit so
// individual engines don't have to repeat it.
func cleanSnippet(text string) string {
	if text == "" {
		return text
	}
	text = snippetNoiseRe.ReplaceAllString(text, " ")
	text = leadingMarkupRe.ReplaceAllString(text, " ")
	text = whitespaceRunRe.ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > snippetMaxRunes {
		runes = runes[:snippetMaxRunes]
	}
	return string(runes)
}
