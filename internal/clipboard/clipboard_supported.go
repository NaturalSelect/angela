//go:build (darwin || linux || windows || freebsd || openbsd || netbsd) && !ios && !android

package clipboard

import "golang.design/x/clipboard"

func initClipboard() error {
	return clipboard.Init()
}

func writeText(text string) {
	clipboard.Write(clipboard.FmtText, []byte(text))
}

func read(f Format) ([]byte, error) {
	// WSLg bridges the Windows host clipboard to the Linux X11/Wayland
	// side for text but not images, so golang.design/x/clipboard never
	// sees an image there. Read it straight from the Windows host instead.
	if f == FormatImage && isWSL() {
		if data, err := readImageWSL(); err == nil {
			return data, nil
		}
	}

	var format clipboard.Format
	switch f {
	case FormatText:
		format = clipboard.FmtText
	case FormatImage:
		format = clipboard.FmtImage
	default:
		return nil, ErrEmpty
	}
	data := clipboard.Read(format)
	if data == nil {
		return nil, ErrEmpty
	}
	return data, nil
}
