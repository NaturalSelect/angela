package message

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAttachment_IsText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mimeType string
		want     bool
	}{
		{name: "text plain", mimeType: "text/plain", want: true},
		{name: "text markdown", mimeType: "text/markdown", want: true},
		{name: "image png", mimeType: "image/png", want: false},
		{name: "empty", mimeType: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := Attachment{MimeType: tt.mimeType}
			require.Equal(t, tt.want, a.IsText())
		})
	}
}

func TestAttachment_IsImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mimeType string
		want     bool
	}{
		{name: "image png", mimeType: "image/png", want: true},
		{name: "image jpeg", mimeType: "image/jpeg", want: true},
		{name: "text plain", mimeType: "text/plain", want: false},
		{name: "empty", mimeType: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := Attachment{MimeType: tt.mimeType}
			require.Equal(t, tt.want, a.IsImage())
		})
	}
}

func TestAttachment_IsMarkdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		mimeType string
		want     bool
	}{
		{name: "text markdown", mimeType: "text/markdown", want: true},
		{name: "text plain", mimeType: "text/plain", want: false},
		{name: "image png", mimeType: "image/png", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a := Attachment{MimeType: tt.mimeType}
			require.Equal(t, tt.want, a.IsMarkdown())
		})
	}
}
