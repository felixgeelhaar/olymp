// Command olymp is the runtime CLI: serve / submit / inspect / steer /
// stream + the explain / fix wrappers.
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		var oe *OlympError
		if errors.As(err, &oe) {
			fmt.Fprintln(os.Stderr, oe.Render(os.Getenv("OLYMP_VERBOSE") == "1"))
			os.Exit(oe.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "olymp:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return printUsage()
	}
	switch args[0] {
	case "serve":
		return cmdServe(args[1:])
	case "submit":
		return cmdSubmit(args[1:])
	case "inspect":
		return cmdInspect(args[1:])
	case "steer":
		return cmdSteer(args[1:])
	case "stream":
		return cmdStream(args[1:])
	case "explain":
		return cmdExplain(args[1:])
	case "fix":
		return cmdFix(args[1:])
	case "halt":
		return cmdHalt(args[1:])
	case "mcp":
		return cmdMCP(args[1:])
	case "seed-demo":
		return cmdSeedDemo(args[1:])
	case "help", "--help", "-h":
		return printUsage()
	default:
		return &OlympError{Code: "unknown_command", Message: "unknown command: " + args[0],
			Hint: "run `olymp help` for the list of supported subcommands"}
	}
}

func printUsage() error {
	fmt.Println(`Olymp — AI runtime for the cognitive stack.

Usage:
  olymp serve   [--addr :8080]
  olymp submit  --type <intent> --subject <subj> [--payload key=val ...]
  olymp inspect <run-id>
  olymp steer   <run-id> <pause|resume|cancel|approve|deny> [--reason ...]
  olymp stream  [--run-id <id>]
  olymp explain <subject>          # alias for submit --type explain
  olymp fix     <subject>          # alias for submit --type remediate
  olymp halt    [--reason ...]     # kill switch: pause every in-flight run
  olymp mcp     [--demo]           # serve MCP over stdio (Claude Code / Codex)

Environment:
  OLYMP_URL       Server base URL (default http://localhost:8080)
  OLYMP_VERBOSE   1 = dump full cause chain on errors`)
	return nil
}
