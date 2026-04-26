// Package tokenhash provides the canonical token-hashing function used across
// Relay's authentication systems (user API tokens, agent enrollment tokens,
// agent long-lived tokens, invite tokens).
//
// All callers MUST use this function so the format stays consistent with the
// documented contract in CLAUDE.md:
//
//	32 random bytes → hex-encode → SHA-256(hex) → hex-encode → store hash in DB
//
// The raw hex string is what the operator/agent presents; only the hash is
// persisted. tokenhash.Hash takes the raw hex string and returns the hex-encoded
// digest suitable for storage and lookup.
package tokenhash

import (
	"crypto/sha256"
	"encoding/hex"
)

// Hash returns the hex-encoded SHA-256 of raw. raw is expected to be the hex
// string returned to the client at issuance; the bytes hashed are the bytes
// of that hex string itself (not the bytes that would result from hex-decoding
// it). See package doc for rationale.
func Hash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
