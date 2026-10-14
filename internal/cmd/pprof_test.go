package cmd

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NaturalSelect/angela/internal/profiling"
	"github.com/stretchr/testify/require"
)

func TestParseProfileFormat(t *testing.T) {
	t.Parallel()

	goroutine := profiling.Profile{Name: "goroutine"}
	heap := profiling.Profile{Name: "heap"}

	tests := []struct {
		name    string
		profile profiling.Profile
		raw     string
		want    profileFormat
		wantErr string
	}{
		{name: "proto", profile: heap, raw: "proto", want: formatProto},
		{name: "text", profile: heap, raw: "text", want: formatText},
		{name: "full goroutine", profile: goroutine, raw: "full", want: formatFull},
		{name: "full on heap", profile: heap, raw: "full", wantErr: "only available for the goroutine"},
		{name: "unknown", profile: heap, raw: "json", wantErr: "unknown format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseProfileFormat(tt.profile, tt.raw)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestProfileFormatDebugLevelAndExtension(t *testing.T) {
	t.Parallel()

	require.Equal(t, 0, formatProto.debugLevel())
	require.Equal(t, 1, formatText.debugLevel())
	require.Equal(t, 2, formatFull.debugLevel())
	require.Equal(t, "pb.gz", formatProto.extension())
	require.Equal(t, "txt", formatText.extension())
	require.Equal(t, "txt", formatFull.extension())
}

func TestPprofCommandRegistersEveryProfile(t *testing.T) {
	t.Parallel()

	for _, p := range profiling.Profiles {
		sub, _, err := pprofCmd.Find([]string{p.Name})
		require.NoError(t, err)
		require.Equal(t, p.Name, sub.Name())
		require.NotNil(t, sub.Flags().Lookup("pid"))
		require.NotNil(t, sub.Flags().Lookup("output"))
		require.Equal(t, p.Timed, sub.Flags().Lookup("seconds") != nil)
		require.Equal(t, !p.Timed, sub.Flags().Lookup("format") != nil)
	}
	list, _, err := pprofCmd.Find([]string{"list"})
	require.NoError(t, err)
	require.Equal(t, "list", list.Name())
}

func TestWriteProfileFileKeepsCompleteProfile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "p.pb.gz")
	require.NoError(t, writeProfileFile(path, strings.NewReader("data")))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "data", string(got))
}

func TestWriteProfileFileRemovesPartialFileOnFailure(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "p.pb.gz")
	err := writeProfileFile(path, io.MultiReader(strings.NewReader("part"), failingReader{}))
	require.Error(t, err)
	require.NoFileExists(t, path)
}

func TestWriteProfileFileUnwritablePath(t *testing.T) {
	t.Parallel()

	err := writeProfileFile(filepath.Join(t.TempDir(), "missing", "p.pb.gz"), strings.NewReader("data"))
	require.ErrorContains(t, err, "failed to create output file")
}

func TestPprofListWithoutProcessesFails(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	var out bytes.Buffer
	pprofListCmd.SetOut(&out)
	err := pprofListCmd.RunE(pprofListCmd, nil)
	require.ErrorIs(t, err, errNoProfileProcess)
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
