package common

import (
	"testing"

	"github.com/NaturalSelect/angela/internal/ui/styles"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestDiffFormatter(t *testing.T) {
	t.Parallel()

	sty := styles.CharmtonePantera()
	dv := DiffFormatter(&sty)
	require.NotNil(t, dv)

	out := dv.Before("a.txt", "line one\nline two\n").
		After("a.txt", "line one\nline TWO\n").
		Width(60).
		String()

	stripped := ansi.Strip(out)
	require.Contains(t, stripped, "line one")
	require.Contains(t, stripped, "line TWO")
}
