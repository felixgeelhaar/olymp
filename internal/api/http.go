package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/felixgeelhaar/olymp/internal/agentdesc"
	"github.com/felixgeelhaar/olymp/internal/domain"
	"github.com/felixgeelhaar/olymp/internal/intent"
	"github.com/felixgeelhaar/olymp/internal/observability"
	"github.com/felixgeelhaar/olymp/internal/ports"
)

// HTTPHandler returns an http.Handler exposing the four OlympAPI verbs.
//
// Routes:
//
//	POST   /v1/runs              Submit
//	GET    /v1/runs/{id}         Inspect
//	POST   /v1/runs/{id}/steer   Steer
//	GET    /v1/runs/stream       Stream (SSE)
//	POST   /v1/halt              Halt (kill switch)
//	GET    /v1/agent-descriptor  agent-go AgentDescriptor
//	GET    /healthz              Health
//
// `health` may be nil. The handler is unauthenticated; auth middleware is the
// host's responsibility.
func HTTPHandler(svc *Service, registry *intent.Registry, health *observability.HealthRegistry) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/runs", handleSubmit(svc))
	mux.HandleFunc("GET /v1/runs/stream", handleStream(svc))
	mux.HandleFunc("GET /v1/runs/{id}", handleInspect(svc))
	mux.HandleFunc("POST /v1/runs/{id}/steer", handleSteer(svc))
	mux.HandleFunc("POST /v1/halt", handleHalt(svc))
	mux.HandleFunc("GET /v1/agent-descriptor", handleAgentDescriptor(registry))
	mux.HandleFunc("GET /healthz", handleHealth(health))
	return mux
}

func handleAgentDescriptor(registry *intent.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		desc, err := agentdesc.Build(r.Context(), registry, "olymp", "Olymp — AI runtime for the cognitive stack.")
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, desc)
	}
}

// HaltRequest is the wire shape of POST /v1/halt.
type HaltRequest struct {
	Reason string `json:"reason"`
}

// HaltResponse reports the runs the kill-switch affected.
type HaltResponse struct {
	Affected []string `json:"affected"`
}

func handleHalt(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req HaltRequest
		_ = json.NewDecoder(r.Body).Decode(&req) // body optional
		ctx := withCallerAndTenant(r)
		ids, err := svc.Halt(ctx, req.Reason)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, HaltResponse{Affected: ids})
	}
}

// SubmitRequest is the wire shape of POST /v1/runs.
type SubmitRequest struct {
	Type    string         `json:"type"`
	Subject string         `json:"subject,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
}

func handleSubmit(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req SubmitRequest
		if err := decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", err)
			return
		}
		ctx := withCallerAndTenant(r)
		run, err := svc.Submit(ctx, domain.Intent{Type: req.Type, Subject: req.Subject, Payload: req.Payload})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, run)
	}
}

func handleInspect(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			writeError(w, http.StatusBadRequest, "missing_id", errors.New("run id required"))
			return
		}
		snap, err := svc.Inspect(withCallerAndTenant(r), id)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, snap)
	}
}

// SteerRequest is the wire shape of POST /v1/runs/:id/steer.
type SteerRequest struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason,omitempty"`
}

func handleSteer(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req SteerRequest
		if err := decode(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_body", err)
			return
		}
		cmd := domain.SteerCommand{
			Kind: req.Kind, Reason: req.Reason,
			Caller: callerFromHeader(r),
		}
		if err := svc.Steer(r.Context(), id, cmd); err != nil {
			writeServiceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleStream serves SSE.
func handleStream(svc *Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			writeError(w, http.StatusInternalServerError, "no_flusher", errors.New("server does not support streaming"))
			return
		}
		filter := domain.RunFilter{
			RunID:      r.URL.Query().Get("run_id"),
			IntentType: r.URL.Query().Get("intent_type"),
		}
		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()
		ch, err := svc.Stream(ctx, filter)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				body, _ := json.Marshal(ev)
				fmt.Fprintf(w, "data: %s\n\n", body)
				flusher.Flush()
			case <-time.After(15 * time.Second):
				// keepalive
				_, _ = w.Write([]byte(": keepalive\n\n"))
				flusher.Flush()
			}
		}
	}
}

func handleHealth(reg *observability.HealthRegistry) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if reg == nil {
			writeJSON(w, http.StatusOK, observability.Health{Status: "ok"})
			return
		}
		writeJSON(w, http.StatusOK, reg.Snapshot())
	}
}

// callerFromHeader extracts the caller from headers. Hosts behind real auth
// inject the resolved CallerRef before handing off; in dev we fall back to
// X-Olymp-Caller-{Type,ID} headers.
func callerFromHeader(r *http.Request) domain.CallerRef {
	t := strings.TrimSpace(r.Header.Get("X-Olymp-Caller-Type"))
	id := strings.TrimSpace(r.Header.Get("X-Olymp-Caller-Id"))
	name := strings.TrimSpace(r.Header.Get("X-Olymp-Caller-Name"))
	if t == "" {
		t = "user"
	}
	if id == "" {
		id = "anonymous"
	}
	return domain.CallerRef{Type: t, ID: id, Name: name}
}

// tenantFromHeader extracts the tenant from headers. Empty values yield the
// zero Tenant (single-tenant default).
func tenantFromHeader(r *http.Request) domain.Tenant {
	return domain.Tenant{
		Org:  strings.TrimSpace(r.Header.Get("X-Olymp-Tenant-Org")),
		Team: strings.TrimSpace(r.Header.Get("X-Olymp-Tenant-Team")),
		User: strings.TrimSpace(r.Header.Get("X-Olymp-Tenant-User")),
	}
}

// withCallerAndTenant composes the two context stamps used by every handler.
func withCallerAndTenant(r *http.Request) context.Context {
	ctx := WithCaller(r.Context(), callerFromHeader(r))
	if t := tenantFromHeader(r); !t.IsZero() {
		ctx = WithTenant(ctx, t)
	}
	return ctx
}

func decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// errorBody is the wire shape of a non-2xx response.
type errorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func writeError(w http.ResponseWriter, status int, code string, err error) {
	writeJSON(w, status, errorBody{Error: err.Error(), Code: code})
}

func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ports.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", err)
	default:
		writeError(w, http.StatusInternalServerError, "internal", err)
	}
}
