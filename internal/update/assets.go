package update

import (
	"bufio"
	"fmt"
	"runtime"
	"strings"
)

type Platform struct {
	OS   string
	Arch string
}

func CurrentPlatform() Platform {
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// NOTE: These follow the archive name template in .goreleaser.yml, which
// title-cases GOOS and names architectures the way uname does.
var (
	archiveOS = map[string]string{
		"linux":   "Linux",
		"darwin":  "Darwin",
		"windows": "Windows",
		"freebsd": "Freebsd",
		"openbsd": "Openbsd",
		"netbsd":  "Netbsd",
		"android": "Android",
	}
	archiveArch = map[string]string{
		"amd64": "x86_64",
		"386":   "i386",
		"arm64": "arm64",
		"arm":   "armv7",
	}
)

// NOTE: Each package format spells a pre-release version differently, so
// packages are matched by their architecture suffix instead of full name.
type packageFormat struct {
	suffix string
	arch   map[string]string
}

var packageFormats = map[string]packageFormat{
	"deb": {
		suffix: "_%s.deb",
		arch:   map[string]string{"amd64": "amd64", "arm64": "arm64", "arm": "armhf", "386": "i386"},
	},
	"rpm": {
		suffix: ".%s.rpm",
		arch:   map[string]string{"amd64": "x86_64", "arm64": "aarch64", "arm": "armv7hl", "386": "i386"},
	},
	"apk": {
		suffix: "_%s.apk",
		arch:   map[string]string{"amd64": "x86_64", "arm64": "aarch64", "arm": "armv7", "386": "x86"},
	},
	"pacman": {
		suffix: "-%s.pkg.tar.zst",
		arch:   map[string]string{"amd64": "x86_64", "arm64": "aarch64", "arm": "armv7h", "386": "i686"},
	},
}

func ArchiveName(tag string, p Platform) (string, error) {
	osName, ok := archiveOS[p.OS]
	if !ok {
		return "", fmt.Errorf("no release archive for operating system %q", p.OS)
	}
	arch, ok := archiveArch[p.Arch]
	if !ok {
		return "", fmt.Errorf("no release archive for architecture %q", p.Arch)
	}
	ext := ".tar.gz"
	if p.OS == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("angela_%s_%s_%s%s", strings.TrimPrefix(tag, "v"), osName, arch, ext), nil
}

func (r Release) FindAsset(name string) (Asset, bool) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

func (r Release) PackageAsset(format, arch string) (Asset, error) {
	f, ok := packageFormats[format]
	if !ok {
		return Asset{}, fmt.Errorf("unsupported package format %q", format)
	}
	name, ok := f.arch[arch]
	if !ok {
		return Asset{}, fmt.Errorf("no %s package for architecture %q", format, arch)
	}
	suffix := fmt.Sprintf(f.suffix, name)
	for _, a := range r.Assets {
		if strings.HasPrefix(a.Name, "angela") && strings.HasSuffix(a.Name, suffix) {
			return a, nil
		}
	}
	return Asset{}, fmt.Errorf("release %s has no %s package for %s", r.TagName, format, arch)
}

func parseChecksums(data string) map[string]string {
	sums := make(map[string]string)
	sc := bufio.NewScanner(strings.NewReader(data))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) != 2 {
			continue
		}
		sums[strings.TrimPrefix(fields[1], "*")] = strings.ToLower(fields[0])
	}
	return sums
}
