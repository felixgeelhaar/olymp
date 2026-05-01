package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/felixgeelhaar/olymp/internal/adapters/chronos"
	"github.com/felixgeelhaar/olymp/internal/adapters/fake"
	"github.com/felixgeelhaar/olymp/internal/adapters/httpx"
	"github.com/felixgeelhaar/olymp/internal/adapters/mnemos"
	"github.com/felixgeelhaar/olymp/internal/adapters/nous"
	"github.com/felixgeelhaar/olymp/internal/adapters/praxis"
	"github.com/felixgeelhaar/olymp/internal/api"
	"github.com/felixgeelhaar/olymp/internal/audit"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/engine"
	"github.com/felixgeelhaar/olymp/internal/eventbus"
	"github.com/felixgeelhaar/olymp/internal/intent"
	"github.com/felixgeelhaar/olymp/internal/observability"
	"github.com/felixgeelhaar/olymp/internal/ports"
	"github.com/felixgeelhaar/olymp/internal/store"
)

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "listen address")
	dbType := fs.String("db", envOr("OLYMP_DB_TYPE", "memory"), "store backend: memory|sqlite|postgres")
	dbConn := fs.String("db-conn", os.Getenv("OLYMP_DB_CONN"), "store connection string")
	demoMode := fs.Bool("demo", false, "use in-process fake cognitive layers (no Mnemos/Chronos/Nous/Praxis required)")
	mnemosURL := fs.String("mnemos-url", envOr("OLYMP_MNEMOS_URL", "http://localhost:8081"), "mnemos base URL")
	chronosURL := fs.String("chronos-url", envOr("OLYMP_CHRONOS_URL", "http://localhost:8082"), "chronos base URL")
	nousURL := fs.String("nous-url", envOr("OLYMP_NOUS_URL", "http://localhost:8083"), "nous base URL")
	praxisURL := fs.String("praxis-url", envOr("OLYMP_PRAXIS_URL", "http://localhost:8084"), "praxis base URL")
	if err := fs.Parse(args); err != nil {
		return &OlympError{Code: "bad_input", Message: err.Error()}
	}

	ctx := context.Background()
	repos, err := store.Open(ctx, store.Config{Type: *dbType, Conn: *dbConn})
	if err != nil {
		return &OlympError{Code: "store_open", Message: "open store: " + err.Error(), Cause: err}
	}
	registry := intent.New(repos.IntentTypes)
	if _, err := registry.RegisterBuiltins(ctx); err != nil {
		return &OlympError{Code: "builtins", Message: err.Error(), Cause: err}
	}

	logger := observability.NewJSON(os.Stderr)
	auditor := audit.New(repos.Audit, nil)
	bus := eventbus.New(256)
	health := observability.NewHealthRegistry()

	var layers ports.Layers
	if *demoMode {
		layers = ports.Layers{
			Mnemos:  &fake.Mnemos{},
			Chronos: &fake.Chronos{},
			Nous:    &fake.Nous{ScriptedDecision: domain.DecisionRef{ID: "demo-decision"}},
			Praxis:  &fake.Praxis{Result: domain.ActionResult{Status: "succeeded"}},
		}
	} else {
		layers = ports.Layers{
			Mnemos:  mnemos.New(httpx.Config{BaseURL: *mnemosURL}),
			Chronos: chronos.New(httpx.Config{BaseURL: *chronosURL}),
			Nous:    nous.New(httpx.Config{BaseURL: *nousURL}),
			Praxis:  praxis.New(httpx.Config{BaseURL: *praxisURL}),
		}
	}

	eng := engine.New(engine.Config{
		Logger: logger, Health: health,
	}, layers, repos, registry, auditor, bus)
	svc := api.NewService(repos, registry, auditor, bus, eng)
	handler := api.HTTPHandler(svc, registry, health)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]any{
			"event": "olymp_serve_starting", "addr": *addr,
			"db": *dbType, "demo": *demoMode,
		})
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintf(os.Stderr, "olymp: serve: %v\n", err)
		}
	}()
	<-stop
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return &OlympError{Code: "shutdown", Message: err.Error(), Cause: err}
	}
	if repos.Close != nil {
		_ = repos.Close()
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
