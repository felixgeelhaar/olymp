#!/usr/bin/env bash
#
# End-to-end demo against the docker-compose stack. Submits an explain
# intent (read-only, exercises Mnemos + Chronos + Nous), then a remediate
# intent (full loop ending in a Praxis action + Mnemos outcome event),
# then walks the resulting provenance chain.
#
# Run after `docker compose -f deploy/docker-compose.yml up --build` once
# every service's healthcheck is green.

set -euo pipefail

BASE="${OLYMP_URL:-http://localhost:8080}"
JQ="${JQ:-jq}"

if ! command -v "$JQ" >/dev/null 2>&1; then
  echo "jq is required for this demo (https://stedolan.github.io/jq/)" >&2
  exit 1
fi

echo "→ /healthz"
curl -fsS "$BASE/healthz" | "$JQ" '{status, layers: [.layers[] | {layer, healthy, circuit_state}]}'

echo
echo "→ /v1/agent-descriptor"
curl -fsS "$BASE/v1/agent-descriptor" | "$JQ" '{name, capabilities: [.capabilities[].name]}'

echo
echo "→ submit explain payments-latency"
curl -fsS -X POST "$BASE/v1/runs" \
  -H "Content-Type: application/json" \
  -H "X-Olymp-Caller-Type: user" \
  -H "X-Olymp-Caller-Id: demo" \
  -d '{"type":"explain","subject":"payments-latency","payload":{"subject":"payments-latency"}}' \
  | "$JQ" '{id, status, iteration, intent: .intent.type}'

echo
echo "→ submit remediate payments-latency"
REMEDIATE=$(curl -fsS -X POST "$BASE/v1/runs" \
  -H "Content-Type: application/json" \
  -H "X-Olymp-Caller-Type: user" \
  -H "X-Olymp-Caller-Id: demo" \
  -d '{"type":"remediate","subject":"payments-latency","payload":{"subject":"payments-latency"}}')
echo "$REMEDIATE" | "$JQ" '{id, status, iteration, intent: .intent.type}'

REMEDIATE_RUN=$(echo "$REMEDIATE" | "$JQ" -r .id)

echo
echo "→ inspect remediate run — provenance chain"
curl -fsS "$BASE/v1/runs/$REMEDIATE_RUN" | "$JQ" '{
  id: .run.id,
  intent: .run.intent.type,
  subject: .run.intent.subject,
  status: .run.status,
  iterations: .run.iteration,
  timeline: [.timeline[] | {iteration, stage, layer: .layer_ref.layer, layer_ref: .layer_ref.id}]
}'

echo
echo "Done. Try:"
echo "  curl $BASE/v1/runs/stream                                    # SSE event feed"
echo "  curl -X POST $BASE/v1/halt -d '{\"reason\":\"demo\"}'           # kill switch"
echo "  curl http://localhost:7777/healthz                           # mnemos direct"
echo "  curl http://localhost:7778/healthz                           # chronos direct"
echo "  curl http://localhost:7780/healthz                           # nous direct"
echo "  curl http://localhost:7781/healthz                           # praxis direct"
