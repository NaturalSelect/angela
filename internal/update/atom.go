package update

import (
	"context"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	githubOrigin = "https://github.com"
	githubRepo   = "NaturalSelect/angela"
	maxPageSize  = 16 << 20
)

// AtomSource lists releases from the repository's Atom feed and release pages
// on github.com instead of the REST API, whose anonymous rate limit is low.
// The feed only carries the most recent releases and gives no pre-release
// flag, so pre-releases are recognised by their tag alone.
var AtomSource Source = &atom{origin: githubOrigin, repo: githubRepo}

type atom struct {
	origin string
	repo   string
}

type atomFeed struct {
	Entries []struct {
		Links []struct {
			Href string `xml:"href,attr"`
		} `xml:"link"`
	} `xml:"entry"`
}

// Matches the download links on a release's asset list, capturing the tag and
// the file name.
var downloadLink = regexp.MustCompile(`href="([^"]*/releases/download/([^"/]+)/([^"/]+))"`)

func (a *atom) repoURL() string {
	return a.origin + "/" + a.repo
}

// Releases implements [Source]. The releases come without assets, which
// [atom.Assets] fills in for the one release that gets installed.
func (a *atom) Releases(ctx context.Context) ([]Release, error) {
	feedURL := a.repoURL() + "/releases.atom"
	body, err := a.get(ctx, feedURL)
	if err != nil {
		return nil, err
	}
	var feed atomFeed
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", feedURL, err)
	}

	var releases []Release
	for _, e := range feed.Entries {
		for _, l := range e.Links {
			_, escaped, ok := strings.Cut(l.Href, "/releases/tag/")
			if !ok {
				continue
			}
			tag, err := url.PathUnescape(escaped)
			if err != nil || tag == "" {
				continue
			}
			releases = append(releases, Release{TagName: tag, HTMLURL: l.Href, Prerelease: IsPrerelease(tag)})
			break
		}
	}
	return releases, nil
}

// Assets implements [AssetSource] by reading the download links off the
// release's asset list page.
func (a *atom) Assets(ctx context.Context, r Release) ([]Asset, error) {
	body, err := a.get(ctx, a.repoURL()+"/releases/expanded_assets/"+url.PathEscape(r.TagName))
	if err != nil {
		return nil, err
	}

	var assets []Asset
	for _, m := range downloadLink.FindAllSubmatch(body, -1) {
		tag, err := url.PathUnescape(string(m[2]))
		if err != nil || tag != r.TagName {
			continue
		}
		name, err := url.PathUnescape(string(m[3]))
		if err != nil {
			continue
		}
		href := html.UnescapeString(string(m[1]))
		if strings.HasPrefix(href, "/") {
			href = a.origin + href
		}
		assets = append(assets, Asset{Name: name, URL: href})
	}
	if len(assets) == 0 {
		return nil, fmt.Errorf("no downloadable files found on the release page of %s, GitHub may have changed the page", r.TagName)
	}
	return assets, nil
}

func (a *atom) get(ctx context.Context, target string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned status %d", target, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxPageSize))
}
