package chat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsListItemMarker(t *testing.T) {
	t.Parallel()

	tests := []struct {
		line string
		want bool
	}{
		{"", false},
		{"- item", true},
		{"* item", true},
		{"+ item", true},
		{"-\titem", true},
		{"-item", false},
		{"-", false},
		{"1. item", true},
		{"12. item", true},
		{"1) item", true},
		{"1.item", false},
		{"1", false},
		{"1.", false},
		{"1234567890. item", false},
		{"prose text", false},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, isListItemMarker(tt.line), "for %q", tt.line)
	}
}

func TestIsHTMLBlockOpener(t *testing.T) {
	t.Parallel()

	tests := []struct {
		line string
		want bool
	}{
		{"", false},
		{"no angle bracket", false},
		{"<", false},
		{"<!-- comment", true},
		{"<? processing", true},
		{"<![CDATA[data", true},
		{"<!DOCTYPE html>", true},
		{"<!1 not a letter", false},
		{"<script>", true},
		{"<Script src=x>", true},
		{"<textarea", true},
		{"<scriptx>", true}, // falls through to the generic open-tag check
		{"<pre>", true},
		{"<style>", true},
		{"<div>", true},
		{"</div>", true},
		{"<3 not html", false},
		{"<-not html", false},
		{"</3", false},
		{"   <div>", true},   // up to 3 leading spaces tolerated
		{"    <div>", false}, // 4 spaces: only 3 are stripped, so '<' is no longer first
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, isHTMLBlockOpener(tt.line), "for %q", tt.line)
	}
}

func TestLineOpensConstruct(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		line string
		want bool
	}{
		{"tab_indented_code", "\tcode", true},
		{"four_space_indented_code", "    code", true},
		{"blank_after_trim", "   ", false},
		{"blockquote", "> quoted", true},
		{"list_item", "- item", true},
		{"table_pipe", "a | b", true},
		{"setext_underline", "----", true},
		{"plain_prose", "just some prose", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, lineOpensConstruct(tt.line))
		})
	}
}

func TestFirstNonBlankLine(t *testing.T) {
	t.Parallel()

	require.Equal(t, "hello", firstNonBlankLine("\n  \nhello\nworld"))
	require.Equal(t, "", firstNonBlankLine(""))
	require.Equal(t, "", firstNonBlankLine("   \n\t\n  "))
}
