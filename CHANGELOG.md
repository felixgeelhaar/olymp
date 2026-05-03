# Changelog

All notable changes to Olymp are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the
project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Releases are tagged and published via tag-triggered CI (GoReleaser →
GHCR). This file is the human-readable summary.

## [Unreleased]

No unreleased changes.

## [0.1.3] — 2026-05-03

End-to-end loop closure + adapter contract translations.

### Added

- **Closed-loop demo (`./deploy/demo-full.sh`)** — broken sample
  services emit Prometheus metrics, the bridge ingests into Chronos,
  Olymp's `remediate` intent runs the full cognitive loop, Praxis
  fires the registered `http_request` capability against
  `/admin/heal`, and the recovery curve crosses the script's <5%
  recovered threshold within 70-90 seconds. Visible live in Grafana,
  recorded as a Mnemos event, replayable from the audit chain.
- **JWT auth wiring (`internal/auth/issuer.go`)** — Olymp self-mints
  a 24-h agent JWT at startup using a shared HMAC secret
  (`OLYMP_AUTH_SECRET_HEX`) and attaches it to every Mnemos call.
  `httpx.Config.TokenSource` callback shared across all four
  cognitive adapters. Static-token escape hatch via
  `OLYMP_MNEMOS_TOKEN`.
- **Cognitive-port adapter translations** — every adapter now speaks
  the upstream's actual HTTP surface:
  - Mnemos: `Recall` → `GET /v1/claims?status=active`, `Append` → `POST /v1/events`, `Get` → client-side scan with `ports.ErrNotFound`.
  - Chronos: `Signals` → `GET /v1/signals?scope_id=…`, scope from
    `OLYMP_CHRONOS_DEFAULT_SCOPE_ID` env or per-call filter.
  - Nous: `Decide` → `POST /v1/extract`, `decision_id` → `DecisionRef`.
  - Praxis: `DryRun` → `POST /v1/actions/{id}/dry-run`. Wire types
    mirror Praxis's CapitalCase default JSON marshaling so
    `DisallowUnknownFields` accepts the request.
- **Subject → action playbook (`nous.PlaybookEntry`)** — JSON file
  loaded via `OLYMP_NOUS_PLAYBOOK_FILE`. Maps goal-description
  substrings to concrete `ActionRequest`s so Praxis has something to
  execute even when the configured Nous extractor returns no
  commitments. Production swaps for an LLM-driven decision stream.
- **`/admin/heal` parks the broken sample services healthy** — error
  rate / latency drop back to baseline immediately and stay there.
  No climb-back. Models a real rollback that holds.
- **`olymp seed-demo` subcommand** — uses Olymp's HMAC issuer to
  mint a Mnemos JWT, then seeds three baseline claims (payments
  SLO, checkout SLO, rollback hypothesis).
- **GitHub Pages site** — landing page at
  https://felixgeelhaar.github.io/olymp/. Hand-rolled HTML/CSS, no
  Jekyll build, deployed via `actions/deploy-pages` on every
  `docs/**` change.

### Fixed

- **Postgres `runs.scope` not-null bug** — `pq.Array(nil)` serialised
  to NULL and broke every `submit` on a cold-start postgres
  deployment. Adapter normalises nil to `[]string{}` before binding.
- **`go mod tidy` drift** after the nous-adapter `google/uuid`
  dependency landed.
- **golangci-lint v2 compat** — bumped CI to action@v9 + linter
  v2.12.1, cleared the resulting 13 pre-existing issues that the
  older v1.62 binary couldn't see (Go 1.26 module config refused to
  load on the v1 binary).

### Compose

- All five cognitive-stack services pulled from GHCR with current
  pins: `mnemos:0.13.0`, `chronos:0.4.0`, `nous:0.3.0`,
  `praxis:0.3.0`, `olymp:0.1.3`. Demo profile adds `flaky-payments`,
  `slow-checkout`, `prom2chronos`, Prometheus 2.55, Grafana 11.3.

## [0.1.2] — earlier

Initial public release of the Olymp runtime — `OlympAPI`, loop
engine, four-port adapters, multi-backend storage, plugin
architecture, MCP runtime surface, distributed scheduler.
