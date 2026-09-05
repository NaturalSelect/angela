//go:build linux && !android

package clipboard

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"strings"
	"time"
)

// wslClipboardImageScript reads the Windows host clipboard image (if
// any) via .NET Windows Forms and prints it as base64-encoded PNG.
// Empty output means the host clipboard held no image.
const wslClipboardImageScript = `Add-Type -AssemblyName System.Windows.Forms; $img = [System.Windows.Forms.Clipboard]::GetImage(); if ($img) { $ms = New-Object System.IO.MemoryStream; $img.Save($ms, [System.Drawing.Imaging.ImageFormat]::Png); [System.Convert]::ToBase64String($ms.ToArray()) }`

// isWSL reports whether the process is running under WSL. WSLg
// exposes WAYLAND_DISPLAY/DISPLAY to every process, so
// golang.design/x/clipboard happily connects, but WSLg's clipboard
// bridge to the Windows host only relays text through that
// connection, never image formats.
func isWSL() bool {
	if _, ok := os.LookupEnv("WSL_DISTRO_NAME"); ok {
		return true
	}
	version, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	v := strings.ToLower(string(version))
	return strings.Contains(v, "microsoft") || strings.Contains(v, "wsl")
}

// readImageWSL reads the Windows host clipboard image by shelling out
// to powershell.exe, mirroring the approach OpenCode's TUI uses for
// the same WSLg limitation.
func readImageWSL() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "powershell.exe", "-NonInteractive", "-NoProfile", "-Command", wslClipboardImageScript).Output()
	if err != nil {
		return nil, ErrEmpty
	}
	encoded := strings.TrimSpace(string(out))
	if encoded == "" {
		return nil, ErrEmpty
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, ErrEmpty
	}
	return data, nil
}
