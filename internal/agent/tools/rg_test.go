package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGetRgIsDisabledUnderTest pins the guard that keeps rg.go from ever
// shelling out to a real ripgrep binary while running under `go test`
// (see the testing.Testing() check in getRg). Every other test in this
// package relies on that guard to stay hermetic, so this documents it
// explicitly.
func TestGetRgIsDisabledUnderTest(t *testing.T) {
	t.Parallel()
	require.Empty(t, getRg(), "getRg must report empty under go test regardless of whether rg is installed")
}

// TestGetRgCmdReturnsNilUnderTest exercises getRgCmd's early return for
// every glob-pattern shape. Because getRg() is forced empty in tests,
// the branch that builds --glob arguments (lines below the name=="" check)
// can never execute under `go test`; only this early return is reachable.
func TestGetRgCmdReturnsNilUnderTest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		globPattern string
	}{
		{"empty pattern", ""},
		{"relative pattern", "*.go"},
		{"absolute pattern", "/internal/**/*.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := getRgCmd(t.Context(), tt.globPattern)
			require.Nil(t, cmd)
		})
	}
}

// TestGetRgSearchCmdReturnsNilUnderTest exercises getRgSearchCmd's early
// return for every argument shape, for the same reason as
// TestGetRgCmdReturnsNilUnderTest above.
func TestGetRgSearchCmdReturnsNilUnderTest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		pattern string
		path    string
		include string
	}{
		{"no include filter", "hello", "/tmp", ""},
		{"with include filter", "hello", "/tmp", "*.go"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cmd := getRgSearchCmd(t.Context(), tt.pattern, tt.path, tt.include)
			require.Nil(t, cmd)
		})
	}
}
