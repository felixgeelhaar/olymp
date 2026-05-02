package auth_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/auth"
)

// secretHex is a deterministic 32-byte hex string used by every test.
const secretHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func mustSecret(t *testing.T) []byte {
	t.Helper()
	s, err := auth.SecretFromHex(secretHex)
	if err != nil {
		t.Fatalf("SecretFromHex: %v", err)
	}
	return s
}

func TestSecretFromHex_RejectsTooShort(t *testing.T) {
	t.Parallel()
	if _, err := auth.SecretFromHex("00"); err == nil {
		t.Fatal("expected error on short secret")
	}
}

func TestSecretFromHex_RejectsNonHex(t *testing.T) {
	t.Parallel()
	if _, err := auth.SecretFromHex(strings.Repeat("zz", 32)); err == nil {
		t.Fatal("expected error on non-hex secret")
	}
}

func TestNewIssuer_RejectsTooShortSecret(t *testing.T) {
	t.Parallel()
	if _, err := auth.NewIssuer(make([]byte, 16)); err == nil {
		t.Fatal("expected error on short secret")
	}
}

func TestIssueAgentToken_ProducesValidHS256(t *testing.T) {
	t.Parallel()
	iss, err := auth.NewIssuer(mustSecret(t))
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	tok, err := iss.IssueAgentToken("olymp", time.Hour)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token parts = %d, want 3", len(parts))
	}

	// Header is HS256 + JWT.
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Typ string `json:"typ"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if header.Alg != "HS256" || header.Typ != "JWT" {
		t.Errorf("header = %+v", header)
	}

	// Claims carry the expected shape.
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var claims auth.Claims
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if claims.Subject != "olymp" || claims.Kind != "agent" {
		t.Errorf("claims sub/knd = %q/%q", claims.Subject, claims.Kind)
	}
	if len(claims.Scopes) != 1 || claims.Scopes[0] != "*" {
		t.Errorf("scopes = %v", claims.Scopes)
	}
	if claims.Issuer != "mnemos" {
		t.Errorf("issuer = %q", claims.Issuer)
	}
	if claims.JTI == "" {
		t.Error("jti empty")
	}
	if claims.ExpiresAt <= claims.IssuedAt {
		t.Errorf("exp/iat = %d/%d", claims.ExpiresAt, claims.IssuedAt)
	}

	// Signature must verify under the same secret.
	mac := hmac.New(sha256.New, mustSecret(t))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if parts[2] != want {
		t.Error("signature mismatch")
	}
}

func TestIssueAgentToken_RejectsEmptySubject(t *testing.T) {
	t.Parallel()
	iss, _ := auth.NewIssuer(mustSecret(t))
	if _, err := iss.IssueAgentToken("", time.Hour); err == nil {
		t.Fatal("expected error")
	}
}

func TestIssueAgentToken_RejectsZeroTTL(t *testing.T) {
	t.Parallel()
	iss, _ := auth.NewIssuer(mustSecret(t))
	if _, err := iss.IssueAgentToken("olymp", 0); err == nil {
		t.Fatal("expected error")
	}
}

func TestRotatingTokenSource_ReusesUntilNearExpiry(t *testing.T) {
	t.Parallel()
	iss, _ := auth.NewIssuer(mustSecret(t))
	rts := auth.NewRotatingTokenSource(iss, "olymp", time.Hour, 10*time.Minute)
	first := rts.Token()
	second := rts.Token()
	if first == "" || first != second {
		t.Errorf("token churned without near-expiry: %q vs %q", first, second)
	}
}

func TestRotatingTokenSource_DefaultsRefreshWhenInvalid(t *testing.T) {
	t.Parallel()
	iss, _ := auth.NewIssuer(mustSecret(t))
	// refreshBefore >= ttl is invalid → constructor falls back to ttl/4.
	rts := auth.NewRotatingTokenSource(iss, "olymp", time.Hour, time.Hour)
	if rts.Token() == "" {
		t.Error("token empty")
	}
}

// Sanity: hex round-trip — protect against accidental encoding changes.
func TestSecretFromHex_RoundTrip(t *testing.T) {
	t.Parallel()
	got, err := auth.SecretFromHex(secretHex)
	if err != nil {
		t.Fatalf("SecretFromHex: %v", err)
	}
	want, _ := hex.DecodeString(secretHex)
	if string(got) != string(want) {
		t.Error("hex round-trip mismatch")
	}
}
