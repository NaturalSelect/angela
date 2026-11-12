package update

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const testRepo = "owner/repo"

// githubFake serves what github.com serves for a repository: the Atom feed,
// the asset list page of each release and the downloads themselves. Every
// request path is recorded.
type githubFake struct {
	srv  *httptest.Server
	mux  *http.ServeMux
	seen []string
}

func newGithubFake(t *testing.T) *githubFake {
	t.Helper()
	g := &githubFake{mux: http.NewServeMux()}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.seen = append(g.seen, r.URL.Path)
		g.mux.ServeHTTP(w, r)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *githubFake) source() *atom {
	return &atom{origin: g.srv.URL, repo: testRepo}
}

func (g *githubFake) feed(tags ...string) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?><feed xmlns="http://www.w3.org/2005/Atom">`)
	for _, tag := range tags {
		fmt.Fprintf(&b, `<entry><title>%[1]s</title><link rel="alternate" type="text/html" href="%[2]s/%[3]s/releases/tag/%[1]s"/></entry>`, tag, g.srv.URL, testRepo)
	}
	b.WriteString(`</feed>`)
	g.mux.HandleFunc("/"+testRepo+"/releases.atom", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(b.String()))
	})
}

// page serves the asset list of tag with a relative link for every file, the
// way github.com does, and serves the files too.
func (g *githubFake) page(tag string, files map[string][]byte) {
	var b strings.Builder
	b.WriteString(`<ul>`)
	for name, data := range files {
		link := fmt.Sprintf("/%s/releases/download/%s/%s", testRepo, tag, name)
		fmt.Fprintf(&b, `<li><a href="%s" rel="nofollow">%s</a></li>`, link, name)
		g.mux.HandleFunc(link, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) })
	}
	b.WriteString(`<li><a href="/` + testRepo + `/archive/refs/tags/` + tag + `.zip">Source code (zip)</a></li></ul>`)
	g.mux.HandleFunc("/"+testRepo+"/releases/expanded_assets/"+tag, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(b.String()))
	})
}

func TestAtomReleases(t *testing.T) {
	t.Parallel()

	g := newGithubFake(t)
	g.feed("v0.0.6-rc2", "v0.0.5", "nightly")

	releases, err := g.source().Releases(t.Context())
	require.NoError(t, err)
	require.Len(t, releases, 3)

	require.Equal(t, "v0.0.6-rc2", releases[0].TagName)
	require.True(t, releases[0].Prerelease)
	require.Equal(t, g.srv.URL+"/"+testRepo+"/releases/tag/v0.0.6-rc2", releases[0].HTMLURL)

	require.Equal(t, "v0.0.5", releases[1].TagName)
	require.False(t, releases[1].Prerelease)

	require.Equal(t, []string{"/" + testRepo + "/releases.atom"}, g.seen)
}

func TestAtomReleases_SelectionSkipsPrereleaseByDefault(t *testing.T) {
	t.Parallel()

	g := newGithubFake(t)
	g.feed("v0.0.6-rc2", "v0.0.5")
	releases, err := g.source().Releases(t.Context())
	require.NoError(t, err)

	require.Equal(t, "v0.0.5", Select("v0.0.4", releases, false).TagName)
	require.Equal(t, "v0.0.6-rc2", Select("v0.0.4", releases, true).TagName)
}

func TestAtomReleases_BadStatus(t *testing.T) {
	t.Parallel()

	g := newGithubFake(t)
	_, err := g.source().Releases(t.Context())
	require.ErrorContains(t, err, "status 404")
}

func TestAtomReleases_BadFeed(t *testing.T) {
	t.Parallel()

	g := newGithubFake(t)
	g.mux.HandleFunc("/"+testRepo+"/releases.atom", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>not a feed"))
	})
	_, err := g.source().Releases(t.Context())
	require.ErrorContains(t, err, "parsing")
}

func TestAtomAssets(t *testing.T) {
	t.Parallel()

	g := newGithubFake(t)
	g.page("v0.0.6", map[string][]byte{
		"angela_0.0.6_Linux_x86_64.tar.gz": []byte("archive"),
		checksumsAsset:                     []byte("sums"),
		checksumsBundleAsset:               []byte("{}"),
	})

	assets, err := g.source().Assets(t.Context(), Release{TagName: "v0.0.6"})
	require.NoError(t, err)
	require.Len(t, assets, 3, "source archive links are not release assets")

	rel := Release{TagName: "v0.0.6", Assets: assets}
	a, ok := rel.FindAsset("angela_0.0.6_Linux_x86_64.tar.gz")
	require.True(t, ok)
	require.Equal(t, g.srv.URL+"/"+testRepo+"/releases/download/v0.0.6/angela_0.0.6_Linux_x86_64.tar.gz", a.URL)
}

func TestAtomAssets_IgnoresOtherTags(t *testing.T) {
	t.Parallel()

	g := newGithubFake(t)
	g.mux.HandleFunc("/"+testRepo+"/releases/expanded_assets/v0.0.6", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<a href="/` + testRepo + `/releases/download/v0.0.5/old.tar.gz">old</a>` +
			`<a href="/` + testRepo + `/releases/download/v0.0.6/new.tar.gz">new</a>`))
	})

	assets, err := g.source().Assets(t.Context(), Release{TagName: "v0.0.6"})
	require.NoError(t, err)
	require.Len(t, assets, 1)
	require.Equal(t, "new.tar.gz", assets[0].Name)
}

func TestAtomAssets_PageWithoutDownloads(t *testing.T) {
	t.Parallel()

	g := newGithubFake(t)
	g.mux.HandleFunc("/"+testRepo+"/releases/expanded_assets/v0.0.6", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<div>nothing here</div>`))
	})

	_, err := g.source().Assets(t.Context(), Release{TagName: "v0.0.6"})
	require.ErrorContains(t, err, "no downloadable files")
}

func TestUpgrade_ThroughAtomSource(t *testing.T) {
	t.Parallel()

	archive := tarGz(t, map[string]string{
		"angela_0.0.6_Linux_x86_64/angela": "new binary",
	})
	g := newGithubFake(t)
	g.feed("v0.0.6", "v0.0.5")
	g.page("v0.0.6", map[string][]byte{
		"angela_0.0.6_Linux_x86_64.tar.gz": archive,
		checksumsAsset:                     []byte(sumLine(archive, "angela_0.0.6_Linux_x86_64.tar.gz")),
		checksumsBundleAsset:               []byte("{}"),
	})

	u, out := newTestUpgrader(t, "v0.0.5", Platform{"linux", "amd64"}, &fakeSystem{})
	u.Source = g.source()

	require.NoError(t, u.Upgrade(t.Context(), false))

	got, err := os.ReadFile(u.Exe)
	require.NoError(t, err)
	require.Equal(t, "new binary", string(got))
	require.Contains(t, out.String(), "Upgrading Angela from v0.0.5 to v0.0.6")
	require.Contains(t, out.String(), "Checksum verified")
	require.NotContains(t, out.String(), "MB", "the page gives no file sizes")
	for _, path := range g.seen {
		require.NotContains(t, path, "/api/", "must not touch the REST API")
	}
}

func TestUpgrade_AtomSourceUpToDateSkipsAssets(t *testing.T) {
	t.Parallel()

	g := newGithubFake(t)
	g.feed("v0.0.5")

	u, out := newTestUpgrader(t, "v0.0.5", Platform{"linux", "amd64"}, &fakeSystem{})
	u.Source = g.source()

	require.NoError(t, u.Upgrade(t.Context(), false))
	require.Contains(t, out.String(), "already up to date")
	require.Equal(t, []string{"/" + testRepo + "/releases.atom"}, g.seen)
}

func TestUpgrade_AtomSourceAssetFailure(t *testing.T) {
	t.Parallel()

	g := newGithubFake(t)
	g.feed("v0.0.6")

	u, _ := newTestUpgrader(t, "v0.0.5", Platform{"linux", "amd64"}, &fakeSystem{})
	u.Source = g.source()

	require.ErrorContains(t, u.Upgrade(t.Context(), false), "failed to list the files of v0.0.6")
}

func sumLine(data []byte, name string) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)
}
