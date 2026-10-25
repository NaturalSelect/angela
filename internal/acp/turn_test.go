package acp

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFileURIPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		uri     string
		windows bool
		want    string
	}{
		{name: "unix path", uri: "file:///work/proj/a.go", want: "/work/proj/a.go"},
		{name: "unix path with escapes", uri: "file:///work/my%20proj/a.go", want: "/work/my proj/a.go"},
		{name: "unix keeps a drive-looking segment", uri: "file:///C:/proj/a.go", want: "/C:/proj/a.go"},
		{name: "not a file uri", uri: "untitled:Untitled-1", want: "untitled:Untitled-1"},
		{name: "plain path", uri: "/work/proj/a.go", want: "/work/proj/a.go"},
		{name: "no path", uri: "file://", want: "file://"},
		{name: "windows drive", uri: "file:///C:/proj/main.go", windows: true, want: `C:\proj\main.go`},
		{name: "windows lower case drive and escapes", uri: "file:///c:/my%20proj/main.go", windows: true, want: `c:\my proj\main.go`},
		{name: "windows localhost host", uri: "file://localhost/C:/proj/main.go", windows: true, want: `C:\proj\main.go`},
		{name: "windows share", uri: "file://server/share/dir/a.go", windows: true, want: `\\server\share\dir\a.go`},
		{name: "windows path without a drive", uri: "file:///proj/main.go", windows: true, want: `\proj\main.go`},
		{name: "windows not a file uri", uri: "untitled:Untitled-1", windows: true, want: "untitled:Untitled-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, fileURIPath(tt.uri, tt.windows))
		})
	}
}
