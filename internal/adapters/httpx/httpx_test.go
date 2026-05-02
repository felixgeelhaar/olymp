package httpx_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/felixgeelhaar/olymp/internal/adapters/httpx"
)

func TestClient_RoundTripJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept=%q", got)
		}
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c := httpx.New(httpx.Config{BaseURL: srv.URL})
	in := map[string]any{"hello": "world"}
	var out map[string]any
	if err := c.Do(context.Background(), "POST", "/echo", in, &out); err != nil {
		t.Fatalf("do: %v", err)
	}
	if out["hello"] != "world" {
		t.Fatalf("round-trip lost data: %+v", out)
	}
}

func TestClient_4xxFailsFast(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		http.Error(w, `{"error":"bad"}`, 400)
	}))
	defer srv.Close()
	c := httpx.New(httpx.Config{BaseURL: srv.URL, MaxAttempts: 5, InitialDelay: time.Millisecond})
	err := c.Do(context.Background(), "GET", "/x", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var he *httpx.HTTPError
	if !errors.As(err, &he) || he.Status != 400 {
		t.Fatalf("err=%v want HTTPError(400)", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls=%d want 1 (4xx fail fast)", got)
	}
}

func TestClient_5xxRetriesThenSucceeds(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			http.Error(w, "boom", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := httpx.New(httpx.Config{BaseURL: srv.URL, MaxAttempts: 5, InitialDelay: time.Millisecond})
	var out map[string]any
	if err := c.Do(context.Background(), "GET", "/x", nil, &out); err != nil {
		t.Fatalf("do: %v", err)
	}
	if out["ok"] != true {
		t.Fatalf("out=%+v", out)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls=%d want 3", got)
	}
}

func TestClient_429Retries(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n < 2 {
			http.Error(w, "rate", http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := httpx.New(httpx.Config{BaseURL: srv.URL, MaxAttempts: 5, InitialDelay: time.Millisecond})
	if err := c.Do(context.Background(), "GET", "/x", nil, nil); err != nil {
		t.Fatalf("do: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("calls=%d want 2", got)
	}
}
