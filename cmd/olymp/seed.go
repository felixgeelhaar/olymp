package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/felixgeelhaar/olymp/internal/auth"
)

// cmdSeedDemo seeds the cognitive stack with the data the broken-services
// demo expects: a handful of operational claims in Mnemos so Recall has
// something to surface during a run.
//
// Chronos doesn't need explicit seeding — prom2chronos posts observations
// scoped to DEMO_CHRONOS_SCOPE_ID, which Olymp's adapter is also configured
// against, so signals show up automatically once metrics start landing.
//
// Idempotent: re-running adds duplicate claims (Mnemos accepts them), but
// nothing breaks.
func cmdSeedDemo(_ []string) error {
	mnemosURL := envOr("OLYMP_MNEMOS_URL", "http://mnemos:7777")
	secretHex := os.Getenv("OLYMP_AUTH_SECRET_HEX")
	if secretHex == "" {
		return &OlympError{Code: "missing_secret", Message: "OLYMP_AUTH_SECRET_HEX must be set"}
	}
	secret, err := auth.SecretFromHex(secretHex)
	if err != nil {
		return &OlympError{Code: "bad_secret", Message: err.Error(), Cause: err}
	}
	issuer, err := auth.NewIssuer(secret)
	if err != nil {
		return &OlympError{Code: "issuer", Message: err.Error(), Cause: err}
	}
	token, err := issuer.IssueAgentToken("olymp-seed", time.Hour)
	if err != nil {
		return &OlympError{Code: "issue", Message: err.Error(), Cause: err}
	}

	claims := []map[string]any{
		{
			"id":         "claim-payments-slo",
			"text":       "payments service SLO: error rate < 5% over 1 minute",
			"type":       "fact",
			"confidence": 0.95,
			"status":     "active",
			"created_at": time.Now().UTC().Format(time.RFC3339),
		},
		{
			"id":         "claim-checkout-slo",
			"text":       "checkout service SLO: p99 latency < 500ms",
			"type":       "fact",
			"confidence": 0.95,
			"status":     "active",
			"created_at": time.Now().UTC().Format(time.RFC3339),
		},
		{
			"id":         "claim-payments-rollback",
			"text":       "payments-latency incidents are typically resolved by rolling back the most recent deploy",
			"type":       "hypothesis",
			"confidence": 0.7,
			"status":     "active",
			"created_at": time.Now().UTC().Format(time.RFC3339),
		},
	}
	body := map[string]any{"claims": claims}
	buf, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(context.Background(), "POST", mnemosURL+"/v1/claims", bytes.NewReader(buf))
	if err != nil {
		return &OlympError{Code: "build_req", Message: err.Error(), Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return &OlympError{Code: "post", Message: "post claims: " + err.Error(), Cause: err}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return &OlympError{Code: "mnemos_reject", Message: fmt.Sprintf("mnemos %d: %s", resp.StatusCode, raw)}
	}
	fmt.Printf("seeded %d claims into mnemos\n", len(claims))
	return nil
}
