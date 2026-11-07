package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	checksumsAsset   = "checksums.txt"
	maxChecksumsSize = 1 << 20
)

func (u *Upgrader) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return http.DefaultClient
}

func (u *Upgrader) get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := u.client().Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("downloading %s: server returned status %d", url, resp.StatusCode)
	}
	return resp, nil
}

func (u *Upgrader) verifier() ChecksumsVerifier {
	if u.Verifier != nil {
		return u.Verifier
	}
	return sigstoreVerifier{}
}

// fetchReleaseFile returns the contents of the small release asset called
// name.
func (u *Upgrader) fetchReleaseFile(ctx context.Context, r Release, name string) ([]byte, error) {
	asset, ok := r.FindAsset(name)
	if !ok {
		return nil, fmt.Errorf("release %s has no %s, so the download cannot be verified", r.TagName, name)
	}
	resp, err := u.get(ctx, asset.URL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, maxChecksumsSize))
}

// expectedChecksum returns the SHA-256 that the release's checksums file
// lists for the asset called name. The checksums file is only trusted once
// its signature has been verified as coming from the release workflow.
func (u *Upgrader) expectedChecksum(ctx context.Context, r Release, name string) (string, error) {
	sums, err := u.fetchReleaseFile(ctx, r, checksumsAsset)
	if err != nil {
		return "", err
	}
	bundle, err := u.fetchReleaseFile(ctx, r, checksumsBundleAsset)
	if err != nil {
		return "", err
	}
	if err := u.verifier().Verify(ctx, r.TagName, sums, bundle); err != nil {
		return "", fmt.Errorf("%s of release %s is not signed by the Angela release workflow: %w", checksumsAsset, r.TagName, err)
	}
	fmt.Fprintf(u.Out, "Verified the signature on %s.\n", checksumsAsset)

	want, ok := parseChecksums(string(sums))[name]
	if !ok {
		return "", fmt.Errorf("%s does not list %s", checksumsAsset, name)
	}
	return want, nil
}

// download saves the asset into dir under its own name and returns the
// path. The file is removed again if its SHA-256 differs from the one in the
// release's checksums file.
func (u *Upgrader) download(ctx context.Context, r Release, a Asset, dir string) (string, error) {
	want, err := u.expectedChecksum(ctx, r, a.Name)
	if err != nil {
		return "", err
	}

	fmt.Fprintf(u.Out, "Downloading %s (%.1f MB).\n", a.Name, float64(a.Size)/(1<<20))
	resp, err := u.get(ctx, a.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	dst := filepath.Join(dir, filepath.Base(a.Name))
	f, err := os.Create(dst)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, hash), resp.Body)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("downloading %s: %w", a.Name, err)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		os.Remove(dst)
		return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %s", a.Name, want, got)
	}
	fmt.Fprintln(u.Out, "Checksum verified.")
	return dst, nil
}

// extractBinary copies the executable called binName out of a tar.gz or zip
// archive into dst.
func extractBinary(archive, binName string, dst io.Writer) error {
	if strings.HasSuffix(archive, ".zip") {
		return extractFromZip(archive, binName, dst)
	}
	return extractFromTarGz(archive, binName, dst)
}

func extractFromTarGz(archive, binName string, dst io.Writer) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s does not contain %s", filepath.Base(archive), binName)
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && path.Base(hdr.Name) == binName {
			_, err := io.Copy(dst, tr)
			return err
		}
	}
}

func extractFromZip(archive, binName string, dst io.Writer) error {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer zr.Close()

	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() || path.Base(zf.Name) != binName {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		_, err = io.Copy(dst, rc)
		return err
	}
	return fmt.Errorf("%s does not contain %s", filepath.Base(archive), binName)
}

// installBinary replaces the running executable with the one in archive.
// The new file is written next to the old one so that the final rename stays
// on one file system and replaces it in a single step.
func (u *Upgrader) installBinary(archive string) error {
	info, err := os.Stat(u.Exe)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(u.Exe), ".angela-upgrade-*")
	if err != nil {
		return fmt.Errorf("cannot write next to %s, run `angela upgrade` with permission to modify it: %w", u.Exe, err)
	}
	defer os.Remove(tmp.Name())

	binName := "angela"
	if u.Platform.OS == "windows" {
		binName += ".exe"
	}
	err = extractBinary(archive, binName, tmp)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), info.Mode().Perm()); err != nil {
		return err
	}
	return u.replaceExecutable(tmp.Name())
}

// replaceExecutable moves newPath over the running executable. Windows
// refuses to overwrite a running program but allows renaming it, so the old
// file is moved aside and deleted on a later run.
func (u *Upgrader) replaceExecutable(newPath string) error {
	if u.Platform.OS != "windows" {
		return os.Rename(newPath, u.Exe)
	}
	old := u.Exe + ".old"
	os.Remove(old)
	if err := os.Rename(u.Exe, old); err != nil {
		return err
	}
	if err := os.Rename(newPath, u.Exe); err != nil {
		os.Rename(old, u.Exe)
		return err
	}
	os.Remove(old)
	return nil
}
