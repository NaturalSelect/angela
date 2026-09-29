package websearch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRouter_SearchManyMergesAndRanks(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]Engine{
		okEngine("bing", Source{URL: "https://a.example", Title: "A"}, Source{URL: "https://b.example", Title: "B"}),
		okEngine("ddg", Source{URL: "https://a.example/", Title: "A dup"}, Source{URL: "https://c.example", Title: "C"}),
		emptyEngine("tavily"),
	}, "bing", 0)
	require.NoError(t, err)

	res, err := r.SearchMany(context.Background(), Request{Query: "go", MaxResults: 10}, []string{"bing", "ddg", "tavily"})
	require.NoError(t, err)
	require.Len(t, res.Sources, 3)
	// a.example was seen by both bing and ddg, so it ranks first.
	require.Equal(t, "https://a.example", res.Sources[0].URL)
	require.ElementsMatch(t, []string{"bing", "ddg"}, res.Sources[0].SeenIn)
	require.ElementsMatch(t, []string{"bing", "ddg"}, res.Used)
}

func TestRouter_SearchManySkipsNotConfigured(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]Engine{
		okEngine("bing", Source{URL: "https://a.example"}),
		errEngine("exa", ErrNotConfigured),
	}, "bing", 0)
	require.NoError(t, err)

	res, err := r.SearchMany(context.Background(), Request{Query: "go"}, []string{"bing", "exa"})
	require.NoError(t, err)
	require.Equal(t, []string{"exa"}, res.Skipped)
	require.Empty(t, res.Failed)
}

func TestRouter_SearchManyDefaultsToTopThree(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]Engine{
		okEngine("bing", Source{URL: "https://a.example"}),
		okEngine("ddg", Source{URL: "https://b.example"}),
		okEngine("tavily", Source{URL: "https://c.example"}),
		okEngine("exa", Source{URL: "https://d.example"}),
	}, "bing", 0)
	require.NoError(t, err)

	res, err := r.SearchMany(context.Background(), Request{Query: "go"}, nil)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"bing", "ddg", "tavily"}, res.Used)
}

func TestRouter_SearchManyRejectsUnknownEnginesOnly(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]Engine{okEngine("bing", Source{URL: "https://a.example"})}, "bing", 0)
	require.NoError(t, err)

	_, err = r.SearchMany(context.Background(), Request{Query: "go"}, []string{"nonexistent"})
	require.Error(t, err)
}

func TestRouter_SearchManyCountsAnEngineOncePerSource(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]Engine{
		okEngine("bing", Source{URL: "https://dup.example/page"}, Source{URL: "https://dup.example/page/"}),
		okEngine("ddg", Source{URL: "https://other.example"}),
	}, "bing", 0)
	require.NoError(t, err)

	res, err := r.SearchMany(context.Background(), Request{Query: "go"}, []string{"bing", "ddg"})
	require.NoError(t, err)
	require.Equal(t, []string{"bing"}, res.Sources[0].SeenIn)
}

func TestRouter_SearchManyDedupesRepeatedEngineIDs(t *testing.T) {
	t.Parallel()
	r, err := NewRouter([]Engine{
		okEngine("bing", Source{URL: "https://a.example"}),
		okEngine("ddg", Source{URL: "https://b.example"}, Source{URL: "https://a.example"}),
	}, "bing", 0)
	require.NoError(t, err)

	res, err := r.SearchMany(context.Background(), Request{Query: "go"}, []string{"bing", "bing", "ddg"})
	require.NoError(t, err)
	require.Equal(t, []string{"bing", "ddg"}, res.Used)
	require.ElementsMatch(t, []string{"bing", "ddg"}, res.Sources[0].SeenIn)
	require.Equal(t, "https://a.example", res.Sources[0].URL)
}
