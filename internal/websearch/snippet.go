package websearch

import (
	"regexp"
	"strings"
)

const snippetNoisePhrases = `sign up|sign in|log in|login|subscribe|member[- ]?only|become a member|create (?:a )?free account|read more|continue reading|story continues|get started|install (?:the )?app|view on|medium membership|join \w+ for free|get updates from this writer|stories in your inbox|remember me for|unlock this|free to read|become a patron`

// boilerplateSentenceRe matches a sentence made up of nothing but a noise
// phrase (plus a short call-to-action tail such as "for free" or "to read
// more"), so a phrase that is part of a real sentence never matches.
var boilerplateSentenceRe = regexp.MustCompile(`(?i)^\W*(?:` + snippetNoisePhrases + `)(?:\s+(?:to (?:read|continue)(?: more| reading)?|for free|free|now|today|here))*\W*$`)

// sentenceBoundaryRe ends a sentence at terminal punctuation followed by
// whitespace, or at a line break. A dot inside a token such as
// "consumer.subscribe" is not a boundary.
var sentenceBoundaryRe = regexp.MustCompile(`[.!?…]+\s+|\n+`)

var leadingMarkupRe = regexp.MustCompile(`(?m)^\s*(#{1,6}\s*|\[\s*x?\s*\]\s*|-\s*\[\s*x?\s*\]\s*|>\s*)`)

var whitespaceRunRe = regexp.MustCompile(`\s+`)

// snippetMaxRunes bounds a cleaned snippet's length.
const snippetMaxRunes = 300

// cleanSnippet drops leading and trailing boilerplate sentences (login,
// paywall and subscription prompts), strips markdown-list and heading
// leftovers, collapses whitespace, and truncates to a bounded length.
// Sentences in the middle are never touched: words like "login" and
// "subscribe" are ordinary vocabulary in technical text. Applied once at
// the Router's exit so individual engines don't have to repeat it.
func cleanSnippet(text string) string {
	if text == "" {
		return text
	}
	text = leadingMarkupRe.ReplaceAllString(text, " ")
	text = trimBoilerplateSentences(text)
	text = whitespaceRunRe.ReplaceAllString(text, " ")
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) > snippetMaxRunes {
		runes = runes[:snippetMaxRunes]
	}
	return string(runes)
}

// trimBoilerplateSentences removes boilerplate sentences from both ends of
// text, stopping at the first sentence that is real content on each side.
func trimBoilerplateSentences(text string) string {
	sentences := splitSentences(text)
	start, end := 0, len(sentences)
	for start < end && boilerplateSentenceRe.MatchString(sentences[start]) {
		start++
	}
	for end > start && boilerplateSentenceRe.MatchString(sentences[end-1]) {
		end--
	}
	return strings.Join(sentences[start:end], "")
}

// splitSentences splits text so that joining the result with "" returns
// text unchanged; each sentence keeps its trailing punctuation and
// whitespace.
func splitSentences(text string) []string {
	var sentences []string
	prev := 0
	for _, loc := range sentenceBoundaryRe.FindAllStringIndex(text, -1) {
		sentences = append(sentences, text[prev:loc[1]])
		prev = loc[1]
	}
	if prev < len(text) {
		sentences = append(sentences, text[prev:])
	}
	return sentences
}
