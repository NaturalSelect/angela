package update

import (
	"bytes"
	"context"
	"fmt"

	"github.com/sigstore/sigstore-go/pkg/bundle"
	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/sigstore/sigstore-go/pkg/tuf"
	"github.com/sigstore/sigstore-go/pkg/verify"
)

const (
	checksumsBundleAsset = checksumsAsset + ".sigstore.json"
	releaseIssuer        = "https://token.actions.githubusercontent.com"
	releaseWorkflowURI   = "https://github.com/NaturalSelect/angela/.github/workflows/release.yml"
)

// ChecksumsVerifier checks that a checksums file was signed by the release
// workflow for a given tag.
type ChecksumsVerifier interface {
	Verify(ctx context.Context, tag string, checksums, bundleJSON []byte) error
}

// sigstoreVerifier verifies a cosign bundle against the Sigstore public-good
// trust root.
type sigstoreVerifier struct {
	// trustedRoot defaults to a TUF fetch of the public-good root.
	trustedRoot func(ctx context.Context) (root.TrustedMaterial, error)
}

func fetchTrustedRoot(ctx context.Context) (root.TrustedMaterial, error) {
	// NOTE: The local cache is disabled so that an upgrade writes nothing
	// under the user's home directory.
	opts := tuf.DefaultOptions().WithDisableLocalCache().WithContext(ctx)
	return root.FetchTrustedRootWithOptions(opts)
}

func (v sigstoreVerifier) Verify(ctx context.Context, tag string, checksums, bundleJSON []byte) error {
	fetch := v.trustedRoot
	if fetch == nil {
		fetch = fetchTrustedRoot
	}
	trusted, err := fetch(ctx)
	if err != nil {
		return fmt.Errorf("cannot fetch the Sigstore trust root: %w", err)
	}

	b := new(bundle.Bundle)
	if err := b.UnmarshalJSON(bundleJSON); err != nil {
		return fmt.Errorf("cannot read the signature bundle: %w", err)
	}

	verifier, err := verify.NewVerifier(
		trusted,
		verify.WithSignedCertificateTimestamps(1),
		verify.WithTransparencyLog(1),
		verify.WithObserverTimestamps(1),
	)
	if err != nil {
		return err
	}

	// NOTE: The tag is part of the signer identity, so a checksums file and
	// bundle from an older release cannot be replayed under a newer tag.
	identity, err := verify.NewShortCertificateIdentity(releaseIssuer, "", releaseWorkflowURI+"@refs/tags/"+tag, "")
	if err != nil {
		return err
	}
	policy := verify.NewPolicy(verify.WithArtifact(bytes.NewReader(checksums)), verify.WithCertificateIdentity(identity))
	if _, err := verifier.Verify(b, policy); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}
	return nil
}
