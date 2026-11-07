package update

import (
	"context"
	_ "embed"
	"testing"

	"github.com/sigstore/sigstore-go/pkg/root"
	"github.com/stretchr/testify/require"
)

//go:embed testdata/checksums.txt
var fixtureChecksums []byte

//go:embed testdata/checksums.txt.sigstore.json
var fixtureBundle []byte

//go:embed testdata/trusted_root.json
var fixtureTrustedRoot []byte

func fixtureVerifier(t *testing.T) sigstoreVerifier {
	t.Helper()
	trusted, err := root.NewTrustedRootFromJSON(fixtureTrustedRoot)
	require.NoError(t, err)
	return sigstoreVerifier{trustedRoot: func(context.Context) (root.TrustedMaterial, error) { return trusted, nil }}
}

func TestSigstoreVerifier_AcceptsSignedChecksums(t *testing.T) {
	t.Parallel()
	require.NoError(t, fixtureVerifier(t).Verify(t.Context(), "v0.0.5", fixtureChecksums, fixtureBundle))
}

func TestSigstoreVerifier_RejectsModifiedChecksums(t *testing.T) {
	t.Parallel()
	tampered := append([]byte(nil), fixtureChecksums...)
	tampered[0] ^= 0x01
	require.Error(t, fixtureVerifier(t).Verify(t.Context(), "v0.0.5", tampered, fixtureBundle))
}

func TestSigstoreVerifier_RejectsOtherTag(t *testing.T) {
	t.Parallel()
	require.Error(t, fixtureVerifier(t).Verify(t.Context(), "v0.0.6", fixtureChecksums, fixtureBundle))
}

func TestSigstoreVerifier_RejectsGarbageBundle(t *testing.T) {
	t.Parallel()
	err := fixtureVerifier(t).Verify(t.Context(), "v0.0.5", fixtureChecksums, []byte("{}"))
	require.ErrorContains(t, err, "signature bundle")
}

func TestSigstoreVerifier_TrustRootFailure(t *testing.T) {
	t.Parallel()
	v := sigstoreVerifier{trustedRoot: func(context.Context) (root.TrustedMaterial, error) { return nil, context.DeadlineExceeded }}
	require.ErrorContains(t, v.Verify(t.Context(), "v0.0.5", fixtureChecksums, fixtureBundle), "trust root")
}
