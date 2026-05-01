package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/felixgeelhaar/olymp/client"
	"github.com/felixgeelhaar/olymp/internal/domain"
)

func newClient() *client.Client {
	return client.New(client.Config{
		BaseURL: envOr("OLYMP_URL", "http://localhost:8080"),
		Caller: domain.CallerRef{
			Type: envOr("OLYMP_CALLER_TYPE", "user"),
			ID:   envOr("OLYMP_CALLER_ID", os.Getenv("USER")),
		},
	})
}

func cmdSubmit(args []string) error {
	fs := flag.NewFlagSet("submit", flag.ContinueOnError)
	intentType := fs.String("type", "", "intent type (required)")
	subject := fs.String("subject", "", "intent subject")
	var pairs stringSlice
	fs.Var(&pairs, "payload", "payload key=value (repeatable)")
	if err := fs.Parse(args); err != nil {
		return &OlympError{Code: "bad_input", Message: err.Error()}
	}
	if *intentType == "" {
		return &OlympError{Code: "missing_arg", Message: "--type is required"}
	}
	payload, err := parsePayload(pairs)
	if err != nil {
		return &OlympError{Code: "bad_input", Message: err.Error()}
	}
	if *subject != "" {
		payload["subject"] = *subject
	}
	c := newClient()
	run, err := c.Submit(context.Background(), domain.Intent{
		Type: *intentType, Subject: *subject, Payload: payload,
	})
	if err != nil {
		return wrapClientErr("submit", err)
	}
	return printJSON(run)
}

func cmdInspect(args []string) error {
	if len(args) == 0 {
		return &OlympError{Code: "missing_arg", Message: "run id is required",
			Hint: "usage: olymp inspect <run-id>"}
	}
	c := newClient()
	snap, err := c.Inspect(context.Background(), args[0])
	if err != nil {
		return wrapClientErr("inspect", err)
	}
	return printJSON(snap)
}

func cmdSteer(args []string) error {
	if len(args) < 2 {
		return &OlympError{Code: "missing_arg", Message: "run id and command are required",
			Hint: "usage: olymp steer <run-id> <pause|resume|cancel|approve|deny> [--reason ...]"}
	}
	fs := flag.NewFlagSet("steer", flag.ContinueOnError)
	reason := fs.String("reason", "", "reason for the steer command")
	if err := fs.Parse(args[2:]); err != nil {
		return &OlympError{Code: "bad_input", Message: err.Error()}
	}
	c := newClient()
	if err := c.Steer(context.Background(), args[0], domain.SteerCommand{
		Kind: args[1], Reason: *reason,
	}); err != nil {
		return wrapClientErr("steer", err)
	}
	return nil
}

func cmdStream(args []string) error {
	fs := flag.NewFlagSet("stream", flag.ContinueOnError)
	runID := fs.String("run-id", "", "filter by run id")
	if err := fs.Parse(args); err != nil {
		return &OlympError{Code: "bad_input", Message: err.Error()}
	}
	ctx := context.Background()
	c := newClient()
	ch, err := c.Stream(ctx, domain.RunFilter{RunID: *runID})
	if err != nil {
		return wrapClientErr("stream", err)
	}
	enc := json.NewEncoder(os.Stdout)
	for ev := range ch {
		_ = enc.Encode(ev)
	}
	return nil
}

func cmdExplain(args []string) error {
	if len(args) == 0 {
		return &OlympError{Code: "missing_arg", Message: "subject or run-<id> is required",
			Hint: "usage: olymp explain <subject>  OR  olymp explain run-<id>"}
	}
	// `olymp explain run-<id>` returns the provenance chain for that run.
	if strings.HasPrefix(args[0], "run-") || strings.HasPrefix(args[0], "Run-") {
		return cmdInspect([]string{args[0]})
	}
	subject := strings.Join(args, " ")
	return cmdSubmit([]string{"--type", "explain", "--subject", subject})
}

func cmdHalt(args []string) error {
	fs := flag.NewFlagSet("halt", flag.ContinueOnError)
	reason := fs.String("reason", "", "reason for halting (recorded in audit)")
	if err := fs.Parse(args); err != nil {
		return &OlympError{Code: "bad_input", Message: err.Error()}
	}
	c := newClient()
	ids, err := c.Halt(context.Background(), *reason)
	if err != nil {
		return wrapClientErr("halt", err)
	}
	return printJSON(map[string]any{"affected": ids})
}

func cmdFix(args []string) error {
	if len(args) == 0 {
		return &OlympError{Code: "missing_arg", Message: "subject is required",
			Hint: "usage: olymp fix <subject>"}
	}
	subject := strings.Join(args, " ")
	return cmdSubmit([]string{"--type", "remediate", "--subject", subject})
}

// stringSlice is a flag.Value for repeatable string flags.
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

func parsePayload(pairs []string) (map[string]any, error) {
	out := map[string]any{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return nil, fmt.Errorf("bad --payload %q (want key=value)", p)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

func wrapClientErr(op string, err error) error {
	var ce *client.Error
	if errors.As(err, &ce) {
		switch ce.Status {
		case 404:
			return &OlympError{Code: "not_found", Message: ce.Body, Cause: err}
		case 502, 503, 504:
			return &OlympError{Code: "server_unreachable", Message: ce.Body, Cause: err}
		}
	}
	return &OlympError{Code: op + "_failed", Message: err.Error(), Cause: err}
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
