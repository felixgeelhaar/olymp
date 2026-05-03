// slow-checkout simulates a service whose latency creeps upward over
// time — the kind of regression that wouldn't trip a binary
// "up/down" alarm but should surface to Olymp via Chronos.
package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	requests = promauto.NewCounter(prometheus.CounterOpts{
		Name: "checkout_requests_total",
		Help: "Total checkout requests.",
	})
	latency = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "checkout_request_duration_seconds",
		Help:    "Checkout request duration.",
		Buckets: []float64{0.05, 0.1, 0.2, 0.5, 1, 2, 5, 10},
	})
)

// baseLatency drifts upward over time. After ~90s of uptime the p99
// is well past any reasonable SLO. After /admin/heal fires, latency
// stays parked at the healthy baseline — modelling a real rollback
// that holds.
func baseLatency(start time.Time, healed bool) time.Duration {
	if healed {
		return 80 * time.Millisecond
	}
	secs := time.Since(start).Seconds()
	base := 80.0 + 18.0*secs // ms — 80→1700ms over 90s
	if base > 2500 {
		base = 2500
	}
	return time.Duration(base) * time.Millisecond
}

func main() {
	addr := envOr("ADDR", ":9102")
	rps := envInt("RPS", 5)
	var (
		start  = time.Now()
		healed bool
		mu     sync.RWMutex
	)
	state := func() (time.Time, bool) {
		mu.RLock()
		defer mu.RUnlock()
		return start, healed
	}
	heal := func() {
		mu.Lock()
		healed = true
		mu.Unlock()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/checkout", func(w http.ResponseWriter, r *http.Request) {
		s, h := state()
		base := baseLatency(s, h)
		jitter := time.Duration(rand.Intn(int(base / 4)))
		dur := base + jitter
		time.Sleep(dur)
		requests.Inc()
		latency.Observe(dur.Seconds())
		fmt.Fprintf(w, `{"status":"ok","took_ms":%d}`, dur.Milliseconds())
	})
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	// /admin/heal models the rollback / restart that returns the
	// service to its healthy latency baseline. Praxis fires this in
	// the demo's acting stage.
	mux.HandleFunc("/admin/heal", func(w http.ResponseWriter, _ *http.Request) {
		heal()
		log.Println("slow-checkout: healed (latency drift reset)")
		fmt.Fprintln(w, `{"status":"healed"}`)
	})

	go func() {
		ticker := time.NewTicker(time.Second / time.Duration(rps))
		defer ticker.Stop()
		client := &http.Client{Timeout: 10 * time.Second}
		for range ticker.C {
			_, _ = client.Get("http://127.0.0.1" + addr + "/checkout") //nolint:bodyclose
		}
	}()

	log.Printf("slow-checkout listening on %s (rps=%d)", addr, rps)
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
