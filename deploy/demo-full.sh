#!/usr/bin/env bash
#
# End-to-end demo: brings up the cognitive stack, two deliberately-misbehaving
# sample services (flaky-payments + slow-checkout), Prometheus, Grafana, and
# the prom2chronos bridge that pipes metric values into Chronos. Seeds Mnemos
# with a few operational claims, then drives Olymp through one explain run
# and one remediate run so the closed loop is visible end-to-end.
#
# What you should see when this script finishes:
#
#   * Grafana dashboard at http://localhost:3000 (anonymous access enabled)
#     showing payments error rate climbing and checkout p99 latency drifting up.
#   * Olymp run summaries with provenance steps that touch all four cognitive
#     layers, including the Chronos signal_ref that came from prom2chronos.
#   * Mnemos events incrementing — each Olymp run writes its outcome back.
#
# Pre-requisites: docker (with compose v2), jq, curl. Configured via deploy/.env.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

ENV_FILE="deploy/.env"
COMPOSE="docker compose -f deploy/docker-compose.yml --env-file ${ENV_FILE}"

if [ ! -f "${ENV_FILE}" ]; then
  cp deploy/.env.example "${ENV_FILE}"
  echo "→ wrote ${ENV_FILE} from template"
fi

echo "→ build the local olymp image (carries the seed-demo subcommand + adapter fixes)"
docker build -q -t ghcr.io/felixgeelhaar/olymp:demo "${ROOT}" >/dev/null
export OLYMP_TAG=demo

echo "→ bring up cognitive stack + demo services (this may take a minute)"
${COMPOSE} --profile demo up -d --build

# Wait for Olymp's healthz to report all four layers green.
echo -n "→ wait for olymp /healthz "
for i in $(seq 1 60); do
  if curl -fsS http://localhost:8080/healthz | jq -e '.status == "ok"' >/dev/null 2>&1; then
    echo "ok"
    break
  fi
  echo -n "."
  sleep 2
  if [ "$i" -eq 60 ]; then
    echo
    echo "olymp /healthz never went green; check 'docker compose logs olymp'"
    exit 1
  fi
done

echo "→ wait for prometheus to scrape the broken services"
PROM_PAYMENTS='up%7Bjob%3D%22flaky-payments%22%7D'
PROM_CHECKOUT='up%7Bjob%3D%22slow-checkout%22%7D'
for i in $(seq 1 30); do
  payments=$(curl -fsS "http://localhost:9090/api/v1/query?query=${PROM_PAYMENTS}" \
    | jq -r '.data.result[0].value[1] // "0"')
  checkout=$(curl -fsS "http://localhost:9090/api/v1/query?query=${PROM_CHECKOUT}" \
    | jq -r '.data.result[0].value[1] // "0"')
  if [ "$payments" = "1" ] && [ "$checkout" = "1" ]; then
    echo "  both targets up"
    break
  fi
  sleep 2
done

echo "→ seed mnemos with operational claims"
${COMPOSE} exec -T olymp /app/olymp seed-demo

echo "→ wait for prom2chronos to land at least one signal in chronos"
SCOPE_ID="$(grep ^DEMO_CHRONOS_SCOPE_ID "${ENV_FILE}" | cut -d= -f2)"
for i in $(seq 1 30); do
  count=$(curl -fsS "http://localhost:7778/v1/signals?scope_id=${SCOPE_ID}" | jq -r '.count // 0')
  if [ "$count" -gt 0 ] 2>/dev/null; then
    echo "  chronos has ${count} signals for scope ${SCOPE_ID}"
    break
  fi
  sleep 2
done

echo
echo "──────────────────────────────────────────────────────────────"
echo "  Open http://localhost:3000  (anonymous viewer access enabled)"
echo "  to watch the broken services degrade in real time."
echo "──────────────────────────────────────────────────────────────"
echo

BASE="http://localhost:8080"

echo "→ submit explain payments-latency"
EXPLAIN=$(curl -fsS -X POST "${BASE}/v1/runs" \
  -H "Content-Type: application/json" \
  -H "X-Olymp-Caller-Type: user" \
  -H "X-Olymp-Caller-Id: demo" \
  -d '{"type":"explain","subject":"payments-latency","payload":{"subject":"payments-latency"}}')
echo "$EXPLAIN" | jq '{id, status, intent: .intent.type}'
EXPLAIN_ID=$(echo "$EXPLAIN" | jq -r .id)

echo
echo "→ submit remediate payments-latency"
REM=$(curl -fsS -X POST "${BASE}/v1/runs" \
  -H "Content-Type: application/json" \
  -H "X-Olymp-Caller-Type: user" \
  -H "X-Olymp-Caller-Id: demo" \
  -d '{"type":"remediate","subject":"payments-latency","payload":{"subject":"payments-latency"}}')
echo "$REM" | jq '{id, status, intent: .intent.type}'
REM_ID=$(echo "$REM" | jq -r .id)

echo
echo "→ inspect remediate run — provenance chain"
curl -fsS "${BASE}/v1/runs/${REM_ID}" | jq '{
  id: .run.id,
  intent: .run.intent.type,
  subject: .run.intent.subject,
  status: .run.status,
  iterations: .run.iteration,
  timeline: [.timeline[] | {iteration, stage, layer: .layer_ref.layer, layer_ref: .layer_ref.id}]
}'

echo
echo "Done. Try:"
echo "  open http://localhost:3000                                 # Grafana dashboard"
echo "  open http://localhost:9090                                 # Prometheus"
echo "  curl ${BASE}/v1/runs/${REM_ID} | jq                        # full run snapshot"
echo "  curl http://localhost:7778/v1/signals?scope_id=${SCOPE_ID} | jq  # raw chronos signals"
echo "  ${COMPOSE} --profile demo down -v                          # tear down + wipe volumes"
