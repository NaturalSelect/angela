package engines

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefault_ReturnsEnginesInFixedOrder(t *testing.T) {
	t.Parallel()
	engines := Default(Options{})
	ids := make([]string, len(engines))
	for i, e := range engines {
		ids[i] = e.ID()
	}
	require.Equal(t, []string{"bing", "ddg", "ddg-lite", "anysearch", "exa", "tavily", "firecrawl", "searxng"}, ids)
}

func TestDefault_BuildsAClientWhenNoneProvided(t *testing.T) {
	t.Parallel()
	engines := Default(Options{})
	require.NotEmpty(t, engines)
}
