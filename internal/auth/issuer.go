// Package auth mints HS256 JWTs that the cognitive-stack services
// (currently Mnemos) accept. Tokens mirror Mnemos's `Claims` shape so
// the same secret on both sides is enough — Olymp does not need to
// import any Mnemos package.
//
// Why a hand-rolled issuer rather than a JWT library: this is one
// HS256 path with a fixed claim set, used at process startup. Pulling
// in `golang-jwt/jwt` would expand the dep graph for ~30 lines of
// signing logic.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Claims mirrors Mnemos's auth.Claims exactly. The JSON field names
// must match — Mnemos's verifier decodes `sub`, `knd`, `scp`, `run`,
// `jti`, `iat`, `exp`, `iss`.
type Claims struct {
	Subject   string   `json:"sub"`
	Kind      string   `json:"knd"`
	Scopes    []string `json:"scp,omitempty"`
	Runs      []string `json:"run,omitempty"`
	JTI       string   `json:"jti"`
	IssuedAt  int64    `json:"iat"`
	ExpiresAt int64    `json:"exp"`
	Issuer    string   `json:"iss"`
}

// Issuer signs Claims with an HMAC-SHA256 secret. The secret should
// be ≥32 bytes; SecretFromHex enforces that on input.
type Issuer struct {
	secret []byte
}

// NewIssuer constructs an Issuer. secret must be at least 32 bytes.
func NewIssuer(secret []byte) (*Issuer, error) {
	if len(secret) < 32 {
		return nil, fmt.Errorf("auth: secret must be ≥32 bytes (got %d)", len(secret))
	}
	return &Issuer{secret: append([]byte(nil), secret...)}, nil
}

// SecretFromHex decodes a hex-encoded secret. Length is checked after
// decoding, so callers don't have to.
func SecretFromHex(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("auth: empty secret")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("auth: secret is not valid hex: %w", err)
	}
	if len(b) < 32 {
		return nil, fmt.Errorf("auth: decoded secret must be ≥32 bytes (got %d)", len(b))
	}
	return b, nil
}

// IssueAgentToken mints a token with subject=agentID, knd="agent",
// and full-access scope. Mirrors Mnemos's IssueAgentToken behaviour:
// `["*"]` scope, no run restriction, signed under the shared secret.
func (i *Issuer) IssueAgentToken(agentID string, ttl time.Duration) (string, error) {
	if agentID == "" {
		return "", errors.New("auth: subject required")
	}
	if ttl <= 0 {
		return "", errors.New("auth: ttl must be positive")
	}
	jti, err := newJTI()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	c := Claims{
		Subject:   agentID,
		Kind:      "agent",
		Scopes:    []string{"*"},
		JTI:       jti,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(ttl).Unix(),
		Issuer:    "mnemos",
	}
	return i.signClaims(c)
}

func (i *Issuer) signClaims(c Claims) (string, error) {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerJSON, _ := json.Marshal(header)
	claimsJSON, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("auth: marshal claims: %w", err)
	}
	signingInput := b64(headerJSON) + "." + b64(claimsJSON)
	mac := hmac.New(sha256.New, i.secret)
	mac.Write([]byte(signingInput))
	sig := mac.Sum(nil)
	return signingInput + "." + b64(sig), nil
}

func b64(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func newJTI() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: jti: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// RotatingTokenSource issues a token, then re-mints whenever the
// current one is within `refreshBefore` of expiring. Designed to be
// passed as `httpx.Config.TokenSource` so adapter clients stay
// agnostic about token lifecycle.
type RotatingTokenSource struct {
	issuer        *Issuer
	subject       string
	ttl           time.Duration
	refreshBefore time.Duration
	now           func() time.Time

	current   string
	expiresAt time.Time
}

// NewRotatingTokenSource wires an issuer + subject + ttl into a
// closure-friendly token source. ttl=24h, refreshBefore=1h is a good
// default for service-to-service traffic.
func NewRotatingTokenSource(issuer *Issuer, subject string, ttl, refreshBefore time.Duration) *RotatingTokenSource {
	if refreshBefore <= 0 || refreshBefore >= ttl {
		refreshBefore = ttl / 4
	}
	return &RotatingTokenSource{
		issuer: issuer, subject: subject, ttl: ttl,
		refreshBefore: refreshBefore,
		now:           time.Now,
	}
}

// Token returns the current token, minting a new one if missing or
// near-expiry. Empty return on signing failure — adapters fall back
// to "no auth header" rather than blocking the call.
func (r *RotatingTokenSource) Token() string {
	now := r.now()
	if r.current != "" && now.Add(r.refreshBefore).Before(r.expiresAt) {
		return r.current
	}
	tok, err := r.issuer.IssueAgentToken(r.subject, r.ttl)
	if err != nil {
		return ""
	}
	r.current = tok
	r.expiresAt = now.Add(r.ttl)
	return r.current
}
