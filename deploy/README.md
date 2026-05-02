# `deploy/` — Olymp + Cognitive Stack on Docker

End-to-end production-shape compose stack for the four-system cognitive
stack (**Mnemos · Chronos · Nous · Praxis**) orchestrated by **Olymp**.
No mocks: every container runs the real upstream binary.

## Topology

```
                ┌──────────────────────────────────────────┐
                │              olymp:8080                  │
                │  Submit / Inspect / Steer / Stream / MCP │
                └──┬─────────────┬───────────────┬─────┬───┘
                   │             │               │     │
            ┌──────▼─────┐ ┌─────▼──────┐ ┌──────▼──┐ ┌▼─────────┐
            │ mnemos:7777│ │chronos:7778│ │nous:8080│ │praxis:8080│
            │   memory   │ │  patterns  │ │decisions│ │ execution │
            └─────┬──────┘ └──────┬─────┘ └────┬────┘ └─────┬─────┘
                  │               │            │            │
                  └───────────────┴────────────┴────────────┘
                                  │
                          ┌───────▼────────┐
                          │  postgres:5432 │
                          │  one DB / svc  │
                          └────────────────┘
```

## Image sources

| Service | Source | Pin |
|---|---|---|
| mnemos | `ghcr.io/felixgeelhaar/mnemos:0.13.0` | `MNEMOS_TAG` env (`.env`) |
| chronos | `ghcr.io/felixgeelhaar/chronos:0.4.0` | `CHRONOS_TAG` env (`.env`) |
| nous | `ghcr.io/felixgeelhaar/nous:0.2.0` | `NOUS_TAG` env (`.env`) |
| praxis | `ghcr.io/felixgeelhaar/praxis:0.1.0` | `PRAXIS_TAG` env (`.env`) |
| olymp | `ghcr.io/felixgeelhaar/olymp:0.1.2` | `OLYMP_TAG` env (`.env`) |

All five layers pull pinned production images from GHCR.

> **Tag format note.** Goreleaser strips the leading `v` from
> `{{ .Version }}` for image tags. GHCR tags look like `0.13.0`, not
> `v0.13.0`. Pin accordingly in `.env`.

## Run it

```bash
cd olymp
cp deploy/.env.example deploy/.env       # edit pins if needed
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up
```

Wait for every service's healthcheck to go green, then in another shell:

```bash
./deploy/demo.sh
```

The demo submits an `explain` intent (read-only, exercises Mnemos +
Chronos + Nous), then a `remediate` intent (full loop, ends in a Praxis
action with a Mnemos outcome event written back), then walks the
provenance chain and shows the agent descriptor.

## Services + ports

| Service | Container port | Host port | Role |
|---|---|---|---|
| postgres | 5432 | 5432 | Shared store, one logical DB per service |
| mnemos | 7777 | 7777 | Memory + knowledge graph |
| chronos | 7778 | 7778 | Time-series + pattern detection |
| nous (HTTP) | 8080 | 7780 | Decision layer |
| nous (gRPC) | 50051 | 50051 | Decision layer (gRPC) |
| praxis | 8080 | 7781 | Capability execution |
| olymp | 8080 | 8080 | Runtime — front door |

## Configuration

Every container is configured via env vars in the compose file. The
`olymp` service knows about the four cognitive layers via:

```yaml
OLYMP_MNEMOS_URL:  http://mnemos:7777
OLYMP_CHRONOS_URL: http://chronos:7778
OLYMP_NOUS_URL:    http://nous:8080
OLYMP_PRAXIS_URL:  http://praxis:8080
```

To bring an external Mnemos / Chronos / Nous / Praxis instance into the
mix, drop the corresponding service from the compose file and override
the URL env var on the `olymp` service.

## Verify

```bash
# Olymp health (includes per-layer status)
curl http://localhost:8080/healthz

# AgentDescriptor for agent-go consumers
curl http://localhost:8080/v1/agent-descriptor

# Live event stream
curl http://localhost:8080/v1/runs/stream

# Per-service health
for p in 7777 7778 7780 7781; do curl -s http://localhost:$p/healthz; done
```

## Full demo with Prometheus + Grafana + broken services

The base stack runs Olymp's loop against empty cognitive-stack data.
For a more compelling walkthrough, the `demo` profile adds two
deliberately-misbehaving sample services, Prometheus, Grafana, and a
`prom2chronos` bridge that turns scraped metric values into Chronos
observations.

```bash
./deploy/demo-full.sh
```

What that script does:

1. Builds a local `olymp:demo` image (carries the `seed-demo`
   subcommand and the latest cognitive-adapter contract translations).
2. `docker compose --profile demo up -d --build` — boots the cognitive
   stack alongside `flaky-payments`, `slow-checkout`, Prometheus 2.55,
   Grafana 11.3, and the bridge.
3. Waits for `/healthz` to go green, Prometheus to scrape both fakes,
   `seed-demo` to install three baseline claims into Mnemos, and
   `prom2chronos` to start posting observations into Chronos.
4. Submits one `explain` and one `remediate` run, prints the
   provenance chain spanning all four layers.

While the script is running:

| URL | Shows |
|---|---|
| http://localhost:3000 | Grafana — anonymous viewer access. The provisioned **Olymp Demo — Broken Services** dashboard renders the live error rate + latency curves. |
| http://localhost:9090 | Prometheus UI — try `payments_requests_total` or the same expressions the dashboard uses. |
| http://localhost:8080/v1/runs/stream | SSE feed of Olymp run state changes. |
| http://localhost:7778/v1/signals?scope_id=… | Raw Chronos signals derived from the bridged observations (signals appear once Chronos's compute pipeline has enough datapoints — typically a couple of minutes after the bridge starts). |

Tear the demo profile down separately:

```bash
docker compose -f deploy/docker-compose.yml --env-file deploy/.env --profile demo down -v
```

## Tear down

```bash
docker compose -f deploy/docker-compose.yml down -v
```

The `-v` flag drops the `pgdata` volume — every layer's state vanishes.
Omit it to keep history across restarts.

## Production notes

- **Per-service roles.** The demo hands every service the same
  `olymp:olymp` Postgres credential. Production should issue one role per
  service with grants restricted to its own database.
- **Secrets.** No env vars in the compose file are secret in this demo;
  for production use Docker secrets or an external secret store.
- **Auth.** Olymp's HTTP surface is unauthenticated by default. Wrap it
  in your hostgateway / OAuth proxy / mTLS layer before exposing it.
- **TLS.** All cross-service traffic is plaintext on the Docker bridge.
  Production deploys terminate TLS at the ingress and use mTLS (or a
  service mesh) between services.
