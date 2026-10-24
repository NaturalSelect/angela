package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NaturalSelect/angela/internal/agent"
	"github.com/stretchr/testify/require"
)

func openOutputFile(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE, 0o644)
	require.NoError(t, err)
	return f
}

// TestSaveRunResult_WritesOnlyFinalText covers the --output path: the
// file holds the final response alone, replacing any earlier content,
// including earlier content longer than the new text.
func TestSaveRunResult_WritesOnlyFinalText(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "out.md")
	require.NoError(t, os.WriteFile(path, []byte("an earlier and much longer result\n"), 0o644))
	f := openOutputFile(t, path)

	var warn bytes.Buffer
	require.NoError(t, saveRunResult(f, "  \nthe verdict\n\n", nil, &warn))
	require.NoError(t, f.Close())

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "the verdict\n", string(got))
	require.Empty(t, warn.String())
}

// TestSaveRunResult_OpeningDoesNotClearTheFile pins that the run can
// still read the file it will replace. `angela run -o notes.md "tighten
// notes.md"` would otherwise start from an empty file.
func TestSaveRunResult_OpeningDoesNotClearTheFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "notes.md")
	require.NoError(t, os.WriteFile(path, []byte("draft\n"), 0o644))

	f := openOutputFile(t, path)
	t.Cleanup(func() { _ = f.Close() })

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "draft\n", string(got))
}

// TestSaveRunResult_FailedRunKeepsThePreviousFile makes sure a failed or
// cancelled run neither destroys the earlier contents nor swallows its
// error. The non-zero exit status is what tells a script not to use it.
func TestSaveRunResult_FailedRunKeepsThePreviousFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "out.md")
	require.NoError(t, os.WriteFile(path, []byte("previous\n"), 0o644))
	f := openOutputFile(t, path)

	var warn bytes.Buffer
	runErr := errors.New("model exploded")
	require.ErrorIs(t, saveRunResult(f, "partial", runErr, &warn), runErr)
	require.NoError(t, f.Close())

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "previous\n", string(got))
	require.Empty(t, warn.String())
}

// TestSaveRunResult_CancelledRunKeepsThePreviousFile pins that a cancelled
// run is a failure for --output: the file keeps its earlier contents, which
// `angela run -o notes.md "tighten notes.md"` would otherwise lose, and the
// exit status says the run did not finish.
func TestSaveRunResult_CancelledRunKeepsThePreviousFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "notes.md")
	require.NoError(t, os.WriteFile(path, []byte("draft\n"), 0o644))
	f := openOutputFile(t, path)

	var warn bytes.Buffer
	require.ErrorIs(t, saveRunResult(f, "", agent.ErrRequestCancelled, &warn), agent.ErrRequestCancelled)
	require.NoError(t, f.Close())

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "draft\n", string(got))
	require.Empty(t, warn.String())
}

// TestSaveRunResult_NoFinalTextClearsFileAndWarns covers a run that
// finished with a blank final response. A script chained with && must not
// carry on with an earlier run's text, and must be told why the file is
// empty.
func TestSaveRunResult_NoFinalTextClearsFileAndWarns(t *testing.T) {
	t.Parallel()

	for name, final := range map[string]string{"empty": "", "blank": " \n\t\n"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "out.md")
			require.NoError(t, os.WriteFile(path, []byte("previous\n"), 0o644))
			f := openOutputFile(t, path)

			var warn bytes.Buffer
			require.NoError(t, saveRunResult(f, final, nil, &warn))
			require.NoError(t, f.Close())

			got, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Empty(t, got)
			require.Contains(t, warn.String(), "no final response")
			require.Contains(t, warn.String(), path)
		})
	}
}

// TestSaveRunResult_NilFileIsNoop covers the default: without --output
// there is nothing to write and the run's error passes straight through.
func TestSaveRunResult_NilFileIsNoop(t *testing.T) {
	t.Parallel()

	var warn bytes.Buffer
	require.NoError(t, saveRunResult(nil, "final", nil, &warn))
	require.NoError(t, saveRunResult(nil, "", nil, &warn))
	require.Empty(t, warn.String(), "no --output means nothing to warn about")

	runErr := errors.New("boom")
	require.ErrorIs(t, saveRunResult(nil, "final", runErr, &warn), runErr)
}
