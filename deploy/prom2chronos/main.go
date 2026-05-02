// prom2chronos polls a small set of PromQL expressions and posts each
// result as a chronos observation (POST /v1/ingest). Bridges the
// "metrics in Prometheus" world to the "structured observations in
// Chronos" world without making either side aware of the other.
//
// Configuration:
//
//	PROMETHEUS_URL    base URL of Prometheus (default http://prometheus:9090)
//	CHRONOS_URL       base URL of Chronos (default http://chronos:7778)
//	SCOPE_ID          chronos scope (uuid). Required.
//	POLL_INTERVAL     seconds between scrapes (default 15)
//
// Each row in `queries` becomes one chronos observation per poll.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// query is a single PromQL → chronos observation mapping.
type query struct {
	// Name is the human label used in logs and chronos `meta.metric`.
	Name string
	// Expr is the PromQL expression evaluated each tick.
	Expr string
	// EntityID is the chronos entity. Pre-generated stable UUIDs so
	// downstream signal queries can target a specific service.
	EntityID uuid.UUID
}

// queries are deliberately hard-coded for the demo: stable
// entity IDs make Mnemos seeds and Grafana annotations cross-reference.
var queries = []query{
	{
		Name:     "payments_error_rate",
		Expr:     `sum(rate(payments_requests_total{status="error"}[1m])) / clamp_min(sum(rate(payments_requests_total[1m])), 0.001)`,
		EntityID: uuid.MustParse("11111111-1111-1111-1111-111111111111"),
	},
	{
		Name:     "checkout_p99_latency_seconds",
		Expr:     `histogram_quantile(0.99, sum(rate(checkout_request_duration_seconds_bucket[1m])) by (le))`,
		EntityID: uuid.MustParse("22222222-2222-2222-2222-222222222222"),
	},
}

func main() {
	promURL := envOr("PROMETHEUS_URL", "http://prometheus:9090")
	chrURL := envOr("CHRONOS_URL", "http://chronos:7778")
	scopeStr := os.Getenv("SCOPE_ID")
	if scopeStr == "" {
		log.Fatal("SCOPE_ID must be set (chronos scope uuid)")
	}
	scopeID, err := uuid.Parse(scopeStr)
	if err != nil {
		log.Fatalf("SCOPE_ID parse: %v", err)
	}
	intervalSec := envInt("POLL_INTERVAL", 15)
	interval := time.Duration(intervalSec) * time.Second

	http.DefaultClient.Timeout = 10 * time.Second
	log.Printf("prom2chronos: scope=%s prom=%s chronos=%s interval=%s",
		scopeID, promURL, chrURL, interval)

	for {
		pollOnce(promURL, chrURL, scopeID)
		time.Sleep(interval)
	}
}

func pollOnce(promURL, chrURL string, scopeID uuid.UUID) {
	now := time.Now().UTC()
	for _, q := range queries {
		val, err := promQuery(promURL, q.Expr)
		if err != nil {
			log.Printf("prom query %q: %v", q.Name, err)
			continue
		}
		if err := postChronos(chrURL, q, scopeID, val, now); err != nil {
			log.Printf("chronos ingest %q: %v", q.Name, err)
			continue
		}
		log.Printf("ok %s = %.4f", q.Name, val)
	}
}

func promQuery(base, expr string) (float64, error) {
	u := base + "/api/v1/query?query=" + url.QueryEscape(expr)
	resp, err := http.Get(u)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("prom %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string          `json:"resultType"`
			Result     []promResultRow `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return 0, err
	}
	if out.Status != "success" {
		return 0, fmt.Errorf("prom status=%q", out.Status)
	}
	if len(out.Data.Result) == 0 {
		return 0, nil // no data yet — emit zero rather than skip
	}
	val, err := parsePromValue(out.Data.Result[0])
	if err != nil {
		return 0, err
	}
	return val, nil
}

type promResultRow struct {
	Metric map[string]string `json:"metric"`
	Value  []any             `json:"value"`
}

func parsePromValue(row promResultRow) (float64, error) {
	if len(row.Value) != 2 {
		return 0, fmt.Errorf("unexpected value shape: %v", row.Value)
	}
	s, ok := row.Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("value not string: %v", row.Value[1])
	}
	if s == "NaN" || s == "+Inf" || s == "-Inf" {
		return 0, nil
	}
	return strconv.ParseFloat(s, 64)
}

type ingestRequest struct {
	EntityID  uuid.UUID         `json:"entity_id"`
	ScopeID   uuid.UUID         `json:"scope_id"`
	Timestamp time.Time         `json:"timestamp"`
	Features  []float64         `json:"features"`
	Meta      map[string]string `json:"meta,omitempty"`
	Adapter   string            `json:"adapter,omitempty"`
}

func postChronos(base string, q query, scopeID uuid.UUID, val float64, ts time.Time) error {
	body := ingestRequest{
		EntityID:  q.EntityID,
		ScopeID:   scopeID,
		Timestamp: ts,
		Features:  []float64{val},
		Adapter:   "prom2chronos",
		Meta:      map[string]string{"metric": q.Name},
	}
	buf, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(context.Background(), "POST", base+"/v1/ingest", bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("chronos %d: %s", resp.StatusCode, raw)
	}
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
