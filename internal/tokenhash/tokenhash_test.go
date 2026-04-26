package tokenhash_test

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"relay/internal/tokenhash"

	"github.com/stretchr/testify/require"
)

func TestHash_DeterministicAndMatchesDocumentedFormula(t *testing.T) {
	raw := "deadbeefcafef00d"

	got := tokenhash.Hash(raw)

	// The documented formula: SHA-256 over the hex-encoded string's bytes,
	// then hex-encode the digest.
	sum := sha256.Sum256([]byte(raw))
	want := hex.EncodeToString(sum[:])

	require.Equal(t, want, got)
}

func TestHash_StableVector(t *testing.T) {
	// Pin output to a known vector so future refactors that drift from the
	// documented formula are caught immediately.
	got := tokenhash.Hash("deadbeefcafef00d")
	// Compute this value: sha256.Sum256([]byte("deadbeefcafef00d")) hex-encoded.
	require.Equal(t, "5fdb6c84438e71dad17a1b7dbf808a30c52d89cfbe8a4156c7361e59ecba7477", got)
}

func TestHash_DistinctInputsProduceDistinctHashes(t *testing.T) {
	a := tokenhash.Hash("token-a")
	b := tokenhash.Hash("token-b")
	require.NotEqual(t, a, b)
}
