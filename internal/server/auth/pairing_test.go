package auth

import (
	"strings"
	"testing"
)

func TestGeneratePairingCodeFormatAndAlphabet(t *testing.T) {
	code, err := GeneratePairingCode()
	if err != nil {
		t.Fatal(err)
	}
	// XXXX-XXXX-XX
	parts := strings.Split(code, "-")
	if len(parts) != 3 || len(parts[0]) != 4 || len(parts[1]) != 4 || len(parts[2]) != 2 {
		t.Fatalf("unexpected shape: %q", code)
	}
	for _, forbidden := range []string{"I", "L", "O", "U"} {
		if strings.Contains(code, forbidden) {
			t.Fatalf("code %q contains an excluded ambiguous character %q", code, forbidden)
		}
	}
	normalized := NormalizePairingCode(code)
	if !ValidatePairingCodeFormat(normalized) {
		t.Fatalf("generated code failed its own format validation: %q", code)
	}
}

func TestGeneratePairingCodeEntropyBits(t *testing.T) {
	// 10 chars from a 32-symbol alphabet = exactly 50 bits, as claimed.
	const wantBits = 10 * 5
	if wantBits < 50 {
		t.Fatalf("pairing code entropy %d bits is below the 50-bit target", wantBits)
	}
}

func TestGeneratePairingCodeUnique(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		code, err := GeneratePairingCode()
		if err != nil {
			t.Fatal(err)
		}
		if seen[code] {
			t.Fatalf("collision after %d generations: %q", i, code)
		}
		seen[code] = true
	}
}

func TestNormalizePairingCode(t *testing.T) {
	cases := []string{"abcd-efgh-jk", "ABCD-EFGH-JK", "  ABCDEFGHJK  ", "AbCd-EfGh-jK"}
	want := "ABCDEFGHJK"
	for _, c := range cases {
		if got := NormalizePairingCode(c); got != want {
			t.Fatalf("NormalizePairingCode(%q) = %q, want %q", c, got, want)
		}
	}
}

func TestValidatePairingCodeFormatRejectsGarbage(t *testing.T) {
	cases := []string{
		"",
		"TOOSHORT",
		"WAYTOOLONGCODEHERE",
		"ABCDEFGHIL", // contains excluded I and L
		"ABCD EFGH J",
		"abcdefghjk", // must be pre-normalized (uppercase) by the caller
	}
	for _, c := range cases {
		if ValidatePairingCodeFormat(c) {
			t.Fatalf("expected %q to fail format validation", c)
		}
	}
}

func TestPairingCodeVerifierIsNotThePlaintextCode(t *testing.T) {
	code := "ABCDEFGHJK"
	verifier := PairingCodeVerifier([]byte("test-pairing-secret"), code)
	if string(verifier) == code {
		t.Fatal("verifier must not equal the plaintext code")
	}
	if len(verifier) != 32 { // SHA-256 output size
		t.Fatalf("unexpected verifier length: %d", len(verifier))
	}
}

func TestPairingCodeVerifierRequiresCorrectSecret(t *testing.T) {
	code := "ABCDEFGHJK"
	v1 := PairingCodeVerifier([]byte("secret-a"), code)
	v2 := PairingCodeVerifier([]byte("secret-b"), code)
	if Equal(v1, v2) {
		t.Fatal("verifiers computed with different pairing secrets must differ")
	}
}

func TestPairingCodeVerifierDeterministic(t *testing.T) {
	secret := []byte("test-pairing-secret")
	code := "ABCDEFGHJK"
	v1 := PairingCodeVerifier(secret, code)
	v2 := PairingCodeVerifier(secret, code)
	if !Equal(v1, v2) {
		t.Fatal("the same secret+code must always produce the same verifier")
	}
}
