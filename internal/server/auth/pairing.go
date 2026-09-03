package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

// crockfordAlphabet is Douglas Crockford's Base32: it excludes I, L, O, U so
// a human can't confuse them with 1, 1, 0, V while reading or typing a
// code. Codes are normalized to uppercase before use.
const crockfordAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// pairingCodeChars * log2(32) = 50 bits of entropy.
const pairingCodeChars = 10

var ErrInvalidPairingCode = errors.New("invalid or expired pairing code")

// GeneratePairingCode returns a new human-enterable code shaped
// XXXX-XXXX-XX (10 Crockford Base32 characters, ~50 bits of entropy). Each
// character is one random byte masked to its low 5 bits (byte & 0x1F):
// since 256 is an exact multiple of 32, this is unbiased with no rejection
// sampling needed.
func GeneratePairingCode() (string, error) {
	buf := make([]byte, pairingCodeChars)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate pairing code: %w", err)
	}
	chars := make([]byte, pairingCodeChars)
	for i, b := range buf {
		chars[i] = crockfordAlphabet[b&0x1F]
	}
	return fmt.Sprintf("%s-%s-%s", chars[0:4], chars[4:8], chars[8:10]), nil
}

// NormalizePairingCode uppercases and strips formatting, so
// "abcd-efgh-jk", "ABCDEFGHJK", and "ABCD-EFGH-JK" all verify identically.
func NormalizePairingCode(raw string) string {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	return strings.ReplaceAll(raw, "-", "")
}

// ValidatePairingCodeFormat checks a normalized code's length and alphabet
// before it's ever hashed or looked up — a cheap rejection of garbage
// input that doesn't need a regex dependency for one fixed-alphabet check.
func ValidatePairingCodeFormat(normalized string) bool {
	if len(normalized) != pairingCodeChars {
		return false
	}
	for i := 0; i < len(normalized); i++ {
		if strings.IndexByte(crockfordAlphabet, normalized[i]) < 0 {
			return false
		}
	}
	return true
}

// PairingCodeVerifier computes the server-secret-keyed verifier for a
// normalized pairing code.
//
// A device token (auth.GenerateToken, 256 random bits) is high-entropy
// enough that a plain SHA-256 hash is standard, sufficient practice — see
// HashToken's doc comment. A pairing code is deliberately human-enterable
// and therefore much lower entropy (~50 bits): a plain hash of it would let
// anyone who obtained only the database (without the separate pairing
// secret) offline-brute-force active codes far faster than the 10-minute
// TTL protects against. Keying the hash with a server-side secret that
// never touches the database — HMAC-SHA256(pairing_secret, code) — means
// the stored verifier reveals nothing about the code without that secret
// too, the same tradeoff a keyed MAC always buys over a bare hash of
// low-entropy input.
func PairingCodeVerifier(pairingSecret []byte, normalizedCode string) []byte {
	mac := hmac.New(sha256.New, pairingSecret)
	mac.Write([]byte(normalizedCode))
	return mac.Sum(nil)
}
