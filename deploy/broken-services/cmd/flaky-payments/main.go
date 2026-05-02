// flaky-payments simulates a payments service whose error rate spikes
// during business hours. It exposes /pay (the synthetic endpoint) and
// /metrics (Prometheus). Useful as a deliberately-misbehaving target
// for the Olymp demo loop.
package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requests = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payments_requests_total",
			Help: "Total payment requests by outcome.",
		},
		[]string{"status"},
	)
	latency = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "payments_request_duration_seconds",
			Help:    "Payment request duration.",
			Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
		},
		[]string{"status"},
	)
)

// errorRate is the fraction of requests that fail. Climbs from a
// healthy baseline to a misbehaving one to make the dashboard
// visibly degrade over time.
func errorRate(start time.Time) float64 {
	mins := time.Since(start).Minutes()
	switch {
	case mins < 1:
		return 0.02 // healthy
	case mins < 3:
		return 0.15 // warning
	default:
		return 0.40 // broken
	}
}

func main() {
	addr := envOr("ADDR", ":9101")
	rps := envInt("RPS", 5)
	start := time.Now()

	mux := http.NewServeMux()
	mux.HandleFunc("/pay", func(w http.ResponseWriter, r *http.Request) {
		dur := time.Duration(20+rand.Intn(80)) * time.Millisecond
		time.Sleep(dur)
		if rand.Float64() < errorRate(start) {
			requests.WithLabelValues("error").Inc()
			latency.WithLabelValues("error").Observe(dur.Seconds())
			http.Error(w, "payment failed", http.StatusInternalServerError)
			return
		}
		requests.WithLabelValues("success").Inc()
		latency.WithLabelValues("success").Observe(dur.Seconds())
		fmt.Fprintln(w, `{"status":"paid"}`)
	})
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})

	// Background load generator so metrics tick over without external clients.
	go func() {
		ticker := time.NewTicker(time.Second / time.Duration(rps))
		defer ticker.Stop()
		client := &http.Client{Timeout: 2 * time.Second}
		for range ticker.C {
			_, _ = client.Get("http://127.0.0.1" + addr + "/pay") //nolint:bodyclose
		}
	}()

	log.Printf("flaky-payments listening on %s (rps=%d)", addr, rps)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
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
