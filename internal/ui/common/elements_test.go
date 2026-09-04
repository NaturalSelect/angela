package common

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestFormatTokensAndCostPrefixesEstimatedUsage(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	rendered := formatTokensAndCost(&sty, 120, 1000, 0, true)
	actual := ansi.Strip(rendered)

	require.Contains(t, actual, "~12%")
	require.Contains(t, actual, "(120)")
	require.Contains(t, actual, "$0.00")
	require.True(t, strings.Contains(rendered, sty.ModelInfo.TokenPercentage.Render("~12%")))
}

func TestFormatTokensAndCostOmitsEstimatedPrefix(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	actual := ansi.Strip(formatTokensAndCost(&sty, 120, 1000, 0, false))

	require.Contains(t, actual, "12%")
	require.NotContains(t, actual, "~12%")
}

func TestPrettyPath(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	out := PrettyPath(&sty, "/tmp/some/path.go", 40)
	require.Contains(t, ansi.Strip(out), "path.go")
}

func TestFormatReasoningEffort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		effort string
		want   string
	}{
		{name: "xhigh gets a custom label", effort: "xhigh", want: "X-High"},
		{name: "a normal level is titlecased", effort: "low", want: "Low"},
		{name: "empty stays empty", effort: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, FormatReasoningEffort(tt.effort))
		})
	}
}

func TestModelInfo(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("the provider fits on the first line", func(t *testing.T) {
		t.Parallel()
		out := ModelInfo(&sty, "GPT-5", "openai", "", nil, 80)
		stripped := ansi.Strip(out)
		require.Contains(t, stripped, "GPT-5")
		require.Contains(t, stripped, "via openai")
	})

	t.Run("the provider wraps to a second line when too narrow", func(t *testing.T) {
		t.Parallel()
		out := ModelInfo(&sty, "GPT-5", "a-very-long-provider-name-indeed", "", nil, 10)
		stripped := ansi.Strip(out)
		require.Contains(t, stripped, "GPT-5")
		require.Contains(t, stripped, "via")
		require.Contains(t, stripped, "indeed", "the tail of the wrapped provider name must survive")
	})

	t.Run("no provider omits the via line", func(t *testing.T) {
		t.Parallel()
		out := ModelInfo(&sty, "GPT-5", "", "", nil, 80)
		require.NotContains(t, ansi.Strip(out), "via")
	})

	t.Run("reasoning info is appended", func(t *testing.T) {
		t.Parallel()
		out := ModelInfo(&sty, "GPT-5", "", "High effort", nil, 80)
		require.Contains(t, ansi.Strip(out), "High effort")
	})

	t.Run("context info appends token and cost usage", func(t *testing.T) {
		t.Parallel()
		out := ModelInfo(&sty, "GPT-5", "", "", &ModelContextInfo{
			ContextUsed:  1200,
			ModelContext: 10000,
			Cost:         0.42,
		}, 80)
		stripped := ansi.Strip(out)
		require.Contains(t, stripped, "1.2K")
		require.Contains(t, stripped, "$0.42")
	})
}

func TestStatus(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	t.Run("renders the icon, title and description", func(t *testing.T) {
		t.Parallel()
		out := Status(&sty, StatusOpts{Icon: "●", Title: "Ready", Description: "all good"}, 40)
		stripped := ansi.Strip(out)
		require.Contains(t, stripped, "●")
		require.Contains(t, stripped, "Ready")
		require.Contains(t, stripped, "all good")
	})

	t.Run("the description is truncated to fit the width", func(t *testing.T) {
		t.Parallel()
		out := Status(&sty, StatusOpts{Title: "T", Description: strings.Repeat("x", 100)}, 20)
		require.LessOrEqual(t, lipgloss.Width(out), 20)
	})

	t.Run("extra content is appended", func(t *testing.T) {
		t.Parallel()
		out := Status(&sty, StatusOpts{Title: "T", ExtraContent: "extra"}, 40)
		require.Contains(t, ansi.Strip(out), "extra")
	})
}

func TestSection(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()

	out := Section(&sty, "Title", 30)
	stripped := ansi.Strip(out)
	require.Contains(t, stripped, "Title")
	require.Contains(t, stripped, "─")

	withInfo := Section(&sty, "Title", 30, "extra", "info")
	require.Contains(t, ansi.Strip(withInfo), "extra info")
}

func TestDialogTitle(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	from, to := lipgloss.Color("#ff0000"), lipgloss.Color("#00ff00")

	t.Run("a title that fits gets a rule appended", func(t *testing.T) {
		t.Parallel()
		out := DialogTitle(&sty, "Settings", 30, from, to)
		require.Contains(t, ansi.Strip(out), "Settings")
	})

	t.Run("a title longer than the width is truncated", func(t *testing.T) {
		t.Parallel()
		out := DialogTitle(&sty, "A Very Long Dialog Title Indeed", 10, from, to)
		require.LessOrEqual(t, lipgloss.Width(ansi.Strip(out)), 10)
	})
}
