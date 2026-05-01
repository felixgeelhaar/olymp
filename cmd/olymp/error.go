package main

import (
	"errors"
	"fmt"
	"strings"
)

// OlympError is the canonical CLI error. main translates it into a stderr
// message + exit code; OLYMP_VERBOSE=1 reveals the full cause chain.
type OlympError struct {
	Code    string
	Message string
	Cause   error
	Hint    string
}

func (e *OlympError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// Unwrap exposes the cause chain.
func (e *OlympError) Unwrap() error { return e.Cause }

// Render formats the error for stderr. When verbose is true, the cause chain
// is unwound into a multi-line form.
func (e *OlympError) Render(verbose bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "olymp: %s: %s", e.Code, e.Message)
	if e.Hint != "" {
		fmt.Fprintf(&b, "\n  hint: %s", e.Hint)
	}
	if verbose && e.Cause != nil {
		b.WriteString("\n  cause chain:")
		for cause := e.Cause; cause != nil; cause = errors.Unwrap(cause) {
			fmt.Fprintf(&b, "\n    - %s", cause.Error())
		}
	}
	return b.String()
}

// ExitCode maps a Code to a stable exit code for shell consumers.
func (e *OlympError) ExitCode() int {
	switch e.Code {
	case "unknown_command", "missing_arg", "bad_input":
		return 64 // EX_USAGE
	case "not_found":
		return 65 // EX_DATAERR
	case "server_unreachable":
		return 69 // EX_UNAVAILABLE
	default:
		return 1
	}
}
