// Package auth implements device credential generation and verification.
// It never invents its own cryptography: random generation uses
// crypto/rand, hashing uses crypto/sha256, and comparison uses
// crypto/subtle.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
)

// tokenPrefix makes device tokens grep-able/redactable in logs and tooling
// without weakening entropy (it precedes, not replaces, the random part).
const tokenPrefix = "alnd_"

const tokenRandomBytes = 32 // 256 bits

var ErrInvalidToken = errors.New("invalid or revoked device token")

// GenerateToken creates a new high-entropy device token. raw is returned to
// the caller exactly once (it is never recoverable from what the server
// stores); hash is the SHA-256 digest the caller should persist instead of
// the token itself.
func GenerateToken() (raw string, hash []byte, err error) {
	buf := make([]byte, tokenRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", nil, fmt.Errorf("generate device token: %w", err)
	}
	raw = tokenPrefix + base64.RawURLEncoding.EncodeToString(buf)
	h := HashToken(raw)
	return raw, h, nil
}

// HashToken returns the SHA-256 digest of a raw token. Hashing an
// already-high-entropy random token (as opposed to a low-entropy password)
// is the standard, sufficient approach used by GitHub/Stripe-style API
// tokens — no adaptive/slow hash is needed here.
func HashToken(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}

// Equal performs a constant-time comparison of two token hashes.
func Equal(a, b []byte) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare(a, b) == 1
}
