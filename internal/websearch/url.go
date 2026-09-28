package websearch

import (
	"net/url"
	"strings"
)

// normalizeURL lowercases the host and strips a trailing slash from the
// path, so two links that differ only by case or a trailing slash
// dedupe as the same source. Falls back to a lowercase, trailing-slash-
// trimmed string when the URL doesn't parse.
func normalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return strings.TrimRight(strings.ToLower(raw), "/")
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}
