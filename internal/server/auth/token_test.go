package auth

import (
	"strings"
	"testing"
)

func TestGenerateTokenUniqueAndPrefixed(t *testing.T) {
	raw1, hash1, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	raw2, hash2, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if raw1 == raw2 {
		t.Fatal("two generated tokens must not collide")
	}
	if !strings.HasPrefix(raw1, tokenPrefix) {
		t.Fatalf("token missing expected prefix: %q", raw1)
	}
	if Equal(hash1, hash2) {
		t.Fatal("hashes of different tokens must differ")
	}
}

func TestHashTokenDeterministic(t *testing.T) {
	raw, hash, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if !Equal(HashToken(raw), hash) {
		t.Fatal("HashToken(raw) must match the hash returned by GenerateToken")
	}
}

func TestEqualRejectsWrongToken(t *testing.T) {
	_, hash, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	if Equal(hash, HashToken("alnd_wrong-token")) {
		t.Fatal("Equal must reject a different token's hash")
	}
}
