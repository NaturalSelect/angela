package chat

import (
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/NaturalSelect/angela/internal/message"
	"github.com/NaturalSelect/angela/internal/session"
	"github.com/NaturalSelect/angela/internal/ui/styles"
)

// exportTimestampFormat is the human-readable timestamp format used in
// the Markdown export header.
const exportTimestampFormat = "2006-01-02 15:04:05 MST"

// maxSlugRunes caps how long a slug derived from a session title can
// get, so a long title cannot produce an unwieldy filename.
const maxSlugRunes = 60

// commonMIMEExtensions gives predictable extensions for the media
// types angela actually produces (screenshots, downloads, pasted
// images), ahead of the broader but less predictable stdlib mime
// table (which, for example, prefers ".jpe" over ".jpg").
var commonMIMEExtensions = map[string]string{
	"image/png":        ".png",
	"image/jpeg":       ".jpg",
	"image/gif":        ".gif",
	"image/webp":       ".webp",
	"image/svg+xml":    ".svg",
	"application/pdf":  ".pdf",
	"text/plain":       ".txt",
	"text/markdown":    ".md",
	"application/json": ".json",
}

// SessionToMarkdown renders sess and its messages as a single Markdown
// document. messages must already be in the order they should appear;
// this function does not sort them.
//
// Any binary attachment or image-producing tool result is written to
// a file under assetsDir and linked from the document using only
// assetsDir's base name (so links stay relative). Callers therefore
// get correct links as long as assetsDir sits next to wherever the
// returned document is ultimately written. The directory is created
// lazily, the first time an asset is actually written, so a session
// with no binary content never creates one.
func SessionToMarkdown(sess session.Session, messages []message.Message, assetsDir string) (string, error) {
	var doc strings.Builder
	writeExportHeader(&doc, sess)

	exp := &sessionExporter{assetsDir: assetsDir}
	toolResults := BuildToolResultMap(messagePointers(messages))

	wroteAny := false
	for i := range messages {
		msg := &messages[i]
		if msg.IsReminder() {
			continue
		}
		section, err := exp.renderMessage(msg, toolResults)
		if err != nil {
			return "", err
		}
		if section == "" {
			continue
		}
		if wroteAny {
			doc.WriteString("\n")
		}
		doc.WriteString(section)
		wroteAny = true
	}

	return doc.String(), nil
}

// writeExportHeader writes the document title and a short metadata
// list describing sess.
func writeExportHeader(doc *strings.Builder, sess session.Session) {
	title := strings.TrimSpace(sess.Title)
	if title == "" {
		title = "Untitled Session"
	}
	fmt.Fprintf(doc, "# %s\n\n", title)
	fmt.Fprintf(doc, "- **Session ID:** %s\n", sess.ID)
	if sess.Agent != "" {
		fmt.Fprintf(doc, "- **Agent:** %s\n", sess.Agent)
	}
	if sess.CreatedAt != 0 {
		fmt.Fprintf(doc, "- **Created:** %s\n", time.Unix(sess.CreatedAt, 0).Local().Format(exportTimestampFormat))
	}
	if sess.UpdatedAt != 0 {
		fmt.Fprintf(doc, "- **Updated:** %s\n", time.Unix(sess.UpdatedAt, 0).Local().Format(exportTimestampFormat))
	}
	doc.WriteString("\n")
}

// messagePointers returns a []*message.Message view over messages, for
// callers (like BuildToolResultMap) that are keyed by message pointer.
func messagePointers(messages []message.Message) []*message.Message {
	ptrs := make([]*message.Message, len(messages))
	for i := range messages {
		ptrs[i] = &messages[i]
	}
	return ptrs
}

// sessionExporter carries the mutable state needed while rendering a
// session to Markdown: the running counter used to name extracted
// binary assets, whether the assets directory has been created yet,
// and a lazily built Styles value needed only to reuse the tool
// message item's clipboard-copy formatting helpers.
type sessionExporter struct {
	assetsDir  string
	assetCount int
	assetsMade bool
	sty        *styles.Styles
}

// styles lazily builds the Styles value passed to newBaseToolMessageItem.
// None of the copy-formatting helpers actually render styled text, but
// the constructor needs a non-nil value to seed its tool animation.
func (e *sessionExporter) styles() *styles.Styles {
	if e.sty == nil {
		s := styles.AngelaTeal()
		e.sty = &s
	}
	return e.sty
}

// renderMessage renders a single message's Markdown section, or ""
// when the message has nothing worth exporting.
func (e *sessionExporter) renderMessage(msg *message.Message, toolResults map[string]message.ToolResult) (string, error) {
	if msg.Role == message.Tool {
		// A Tool-role message only carries results, which are
		// rendered inline with the tool call that produced them (see
		// the ToolCalls loop below); it has nothing of its own to
		// show.
		return "", nil
	}

	var sections []string

	if thinking := strings.TrimSpace(msg.ReasoningContent().Thinking); thinking != "" {
		sections = append(sections, renderReasoning(msg, thinking))
	}

	if text := strings.TrimSpace(msg.Content().Text); text != "" {
		sections = append(sections, text)
	}

	for _, sc := range msg.ShellCommands() {
		sections = append(sections, renderShellCommand(sc))
	}

	for _, img := range msg.ImageURLContent() {
		sections = append(sections, fmt.Sprintf("![](%s)", img.URL))
	}

	for _, bin := range msg.BinaryContent() {
		part, err := e.renderBinaryContent(bin)
		if err != nil {
			return "", err
		}
		sections = append(sections, part)
	}

	for _, tc := range msg.ToolCalls() {
		var result *message.ToolResult
		if r, ok := toolResults[tc.ID]; ok {
			result = &r
		}
		part, err := e.renderToolCall(tc, result)
		if err != nil {
			return "", err
		}
		sections = append(sections, part)
	}

	if len(sections) == 0 {
		return "", nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "## %s\n\n", roleHeading(msg.Role))
	b.WriteString(strings.Join(sections, "\n\n"))
	b.WriteString("\n")
	return b.String(), nil
}

// roleHeading returns the section heading text for role.
func roleHeading(role message.MessageRole) string {
	switch role {
	case message.User:
		return "User"
	case message.Assistant:
		return "Assistant"
	case message.System:
		return "System"
	case message.Tool:
		return "Tool"
	default:
		return string(role)
	}
}

// renderReasoning renders an assistant's thinking as a collapsed
// details block so it does not dominate the exported transcript.
func renderReasoning(msg *message.Message, thinking string) string {
	summary := "Thought"
	if d := msg.ThinkingDuration(); d > 0 {
		summary = fmt.Sprintf("Thought for %ds", int(d.Seconds()))
	}
	return fmt.Sprintf("<details>\n<summary>%s</summary>\n\n%s\n\n</details>", summary, thinking)
}

// renderShellCommand renders a bang-mode shell command and its output
// as a fenced shell block.
func renderShellCommand(sc message.ShellCommand) string {
	var b strings.Builder
	b.WriteString("```shell\n")
	fmt.Fprintf(&b, "$ %s\n", sc.Command)
	if sc.Output != "" {
		b.WriteString(sc.Output)
		if !strings.HasSuffix(sc.Output, "\n") {
			b.WriteString("\n")
		}
	}
	b.WriteString("```")
	return b.String()
}

// renderBinaryContent writes an attachment's bytes to the assets
// directory and returns a Markdown image or link tag pointing at it.
func (e *sessionExporter) renderBinaryContent(bin message.BinaryContent) (string, error) {
	base := bin.Path
	if strings.TrimSpace(base) == "" {
		base = "attachment"
	}
	link, err := e.writeAsset(base, bin.MIMEType, bin.Data)
	if err != nil {
		return "", fmt.Errorf("exporting attachment: %w", err)
	}
	return mediaLink(link, bin.MIMEType), nil
}

// renderToolCall renders a tool call and its paired result (if any).
// It reuses formatParametersForCopy and formatResultForCopy — the same
// helpers behind the in-app "copy tool" keybind — so a tool's exported
// form matches what the user would get from copying it inside the
// TUI. The one addition is that a result carrying binary Data is
// written out as a real file instead of the clipboard's
// "[Image: ...]" placeholder.
func (e *sessionExporter) renderToolCall(tc message.ToolCall, result *message.ToolResult) (string, error) {
	item := newBaseToolMessageItem(e.styles(), tc, result, nil, false)

	var b strings.Builder
	fmt.Fprintf(&b, "### Tool: %s\n\n", prettifyToolName(tc.Name))

	if tc.Input != "" {
		if params := item.formatParametersForCopy(); params != "" {
			b.WriteString("**Parameters:**\n\n")
			b.WriteString(params)
			b.WriteString("\n")
		}
	}

	switch {
	case result != nil && result.IsError:
		b.WriteString("\n**Error:**\n\n")
		b.WriteString(result.Content)
	case result != nil && result.Data != "":
		asset, err := e.writeToolResultAsset(tc, *result)
		if err != nil {
			return "", err
		}
		b.WriteString("\n**Result:**\n\n")
		b.WriteString(asset)
	case result != nil:
		if content := item.formatResultForCopy(); content != "" {
			b.WriteString("\n**Result:**\n\n")
			b.WriteString(content)
		}
	default:
		// A static export has no notion of "still running": an
		// unresolved tool call in a persisted session is reported the
		// same way the non-interactive `session show` CLI output
		// treats one (see outputSessionHuman's runActive = false).
		b.WriteString("\n**Status:** Cancelled")
	}

	return b.String(), nil
}

// writeToolResultAsset decodes a tool result's base64 Data and writes
// it to the assets directory, returning a Markdown image or link tag.
func (e *sessionExporter) writeToolResultAsset(tc message.ToolCall, result message.ToolResult) (string, error) {
	data, err := base64.StdEncoding.DecodeString(result.Data)
	if err != nil {
		return "", fmt.Errorf("decoding %s result data: %w", tc.Name, err)
	}
	link, err := e.writeAsset(prettifyToolName(tc.Name), result.MIMEType, data)
	if err != nil {
		return "", fmt.Errorf("exporting %s result: %w", tc.Name, err)
	}
	return mediaLink(link, result.MIMEType), nil
}

// mediaLink formats link as a Markdown image tag for image MIME types
// and a plain link otherwise.
func mediaLink(link, mimeType string) string {
	label := filepath.Base(link)
	if strings.HasPrefix(mimeType, "image/") {
		return fmt.Sprintf("![%s](%s)", label, link)
	}
	return fmt.Sprintf("[%s](%s)", label, link)
}

// writeAsset lazily creates the assets directory on first use, then
// writes data to a new numbered file inside it (e.g.
// "001-screenshot.png"), returning the relative path to embed in a
// Markdown link.
func (e *sessionExporter) writeAsset(baseName, mimeType string, data []byte) (string, error) {
	if err := e.ensureAssetsDir(); err != nil {
		return "", err
	}
	e.assetCount++

	name := sanitizeFileName(filepath.Base(strings.TrimSpace(baseName)))
	if name == "" {
		name = "asset"
	}
	if filepath.Ext(name) == "" {
		name += extensionForMIME(mimeType)
	}
	fileName := fmt.Sprintf("%03d-%s", e.assetCount, name)

	if err := os.WriteFile(filepath.Join(e.assetsDir, fileName), data, 0o644); err != nil {
		return "", fmt.Errorf("writing asset %q: %w", fileName, err)
	}

	return path.Join(filepath.Base(e.assetsDir), fileName), nil
}

// ensureAssetsDir creates the assets directory the first time it is
// needed, and is a no-op afterward.
func (e *sessionExporter) ensureAssetsDir() error {
	if e.assetsMade {
		return nil
	}
	if err := os.MkdirAll(e.assetsDir, 0o755); err != nil {
		return fmt.Errorf("creating assets directory %q: %w", e.assetsDir, err)
	}
	e.assetsMade = true
	return nil
}

// extensionForMIME returns a filename extension (including the
// leading dot) for mimeType, preferring a small table of the types
// angela actually produces before falling back to the standard
// library's registry, and finally ".bin".
func extensionForMIME(mimeType string) string {
	mimeType = strings.TrimSpace(mimeType)
	if idx := strings.IndexByte(mimeType, ';'); idx >= 0 {
		mimeType = strings.TrimSpace(mimeType[:idx])
	}
	mimeType = strings.ToLower(mimeType)
	if mimeType == "" {
		return ".bin"
	}
	if ext, ok := commonMIMEExtensions[mimeType]; ok {
		return ext
	}
	if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

// sanitizeFileName strips characters that are unsafe in a filename on
// common filesystems, replacing each with a hyphen.
func sanitizeFileName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, name)
	return strings.Trim(name, "-")
}

// SessionExportSlug derives a filesystem-safe filename stem for
// exporting sess: a slugified form of its title, falling back to the
// session ID when the title is empty or slugifies to nothing (e.g. a
// title written entirely in a script slugify drops).
func SessionExportSlug(title, sessionID string) string {
	if slug := slugify(title); slug != "" {
		return slug
	}
	if slug := slugify(sessionID); slug != "" {
		return slug
	}
	return "session"
}

// slugify lowercases s and keeps only ASCII letters and digits,
// collapsing every other run of characters into a single hyphen. It
// is deliberately ASCII-only so the result is a safe filename stem on
// every filesystem angela targets.
func slugify(s string) string {
	var b strings.Builder
	prevDash := true // Avoids ever writing a leading dash.
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > maxSlugRunes {
		slug = strings.Trim(slug[:maxSlugRunes], "-")
	}
	return slug
}

// AssetsDirName returns the sibling directory used to hold binary
// assets for a Markdown export at mdPath, e.g. "notes.md" becomes
// "notes.assets".
func AssetsDirName(mdPath string) string {
	ext := filepath.Ext(mdPath)
	return strings.TrimSuffix(mdPath, ext) + ".assets"
}

// UniqueExportPath returns targetPath unchanged if nothing exists
// there yet, otherwise the first sibling with a "-2", "-3", ... suffix
// inserted before the extension that does not exist, so a repeated
// export never silently overwrites an earlier one.
func UniqueExportPath(targetPath string) (string, error) {
	if _, err := os.Stat(targetPath); errors.Is(err, os.ErrNotExist) {
		return targetPath, nil
	} else if err != nil {
		return "", err
	}

	ext := filepath.Ext(targetPath)
	stem := strings.TrimSuffix(targetPath, ext)
	const maxAttempts = 10000
	for n := 2; n < maxAttempts; n++ {
		candidate := fmt.Sprintf("%s-%d%s", stem, n, ext)
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("chat: could not find a unique export path for %q", targetPath)
}
