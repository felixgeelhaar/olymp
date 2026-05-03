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

echo "→ pull pinned cognitive-stack images and bring up demo services"
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
PROM_PAYMENTS_ERR='sum(rate(payments_requests_total%7Bstatus%3D%22error%22%7D%5B1m%5D))%20%2F%20clamp_min(sum(rate(payments_requests_total%5B1m%5D))%2C%200.001)'
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


echo "→ let the broken services degrade for ~80s so the incident is real"
for i in $(seq 1 8); do
  sleep 10
  err=$(curl -fsS "http://localhost:9090/api/v1/query?query=${PROM_PAYMENTS_ERR}" \
    | jq -r '.data.result[0].value[1] // "0"')
  pct=$(awk "BEGIN { print ${err:-0} * 100 }")
  echo "  t+${i}0s payments error rate = $(printf %.2f "$pct")%"
done

SCOPE_ID="$(grep ^DEMO_CHRONOS_SCOPE_ID "${ENV_FILE}" | cut -d= -f2)"

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
echo "→ baseline payments error rate before remediate"
ERR_BEFORE=$(curl -fsS "http://localhost:9090/api/v1/query?query=${PROM_PAYMENTS_ERR}" \
  | jq -r '.data.result[0].value[1] // "0"')
printf "  %.2f%%\n" "$(awk "BEGIN { print ${ERR_BEFORE:-0} * 100 }")"

echo
echo "→ submit remediate payments-latency"
REM=$(curl -fsS -X POST "${BASE}/v1/runs" \
  -H "Content-Type: application/json" \
  -H "X-Olymp-Caller-Type: user" \
  -H "X-Olymp-Caller-Id: demo" \
  -d '{"type":"remediate","subject":"payments-latency","payload":{"subject":"payments-latency"}}')
echo "$REM" | jq '{id, status, intent: .intent.type}'
REM_ID=$(echo "$REM" | jq -r .id)

# Remediate gates on approval: poll until awaiting_approval, then
# approve via /v1/runs/{id}/steer, then poll until completed.
echo
echo -n "→ wait for awaiting_approval"
for i in $(seq 1 30); do
  STATUS=$(curl -fsS "${BASE}/v1/runs/${REM_ID}" | jq -r '.run.status')
  if [ "$STATUS" = "awaiting_approval" ]; then
    echo " ok"
    break
  fi
  if [ "$STATUS" = "completed" ] || [ "$STATUS" = "failed" ]; then
    echo " (already $STATUS)"
    break
  fi
  echo -n "."
  sleep 1
done

if [ "$STATUS" = "awaiting_approval" ]; then
  echo "→ approve the proposed action"
  curl -fsS -X POST "${BASE}/v1/runs/${REM_ID}/steer" \
    -H "Content-Type: application/json" \
    -H "X-Olymp-Caller-Type: user" \
    -H "X-Olymp-Caller-Id: demo" \
    -d '{"kind":"approve","reason":"demo-auto-approve"}'

  echo -n "→ wait for run to complete"
  for i in $(seq 1 30); do
    STATUS=$(curl -fsS "${BASE}/v1/runs/${REM_ID}" | jq -r '.run.status')
    if [ "$STATUS" = "completed" ] || [ "$STATUS" = "failed" ]; then
      echo " $STATUS"
      break
    fi
    echo -n "."
    sleep 1
  done
fi

echo
echo "→ inspect remediate run — provenance chain"
curl -fsS "${BASE}/v1/runs/${REM_ID}" | jq '{
  id: .run.id,
  intent: .run.intent.type,
  subject: .run.intent.subject,
  status: .run.status,
  iterations: .run.iteration,
  timeline: [.timeline[] | {iteration, stage, layer: .layer_ref.layer, layer_ref: .layer_ref.id, outputs: .outputs}]
}'

echo
echo "→ wait for prometheus to record the recovery (~30s)"
for i in $(seq 1 12); do
  ERR_AFTER=$(curl -fsS "http://localhost:9090/api/v1/query?query=${PROM_PAYMENTS_ERR}" \
    | jq -r '.data.result[0].value[1] // "1"')
  AFTER_PCT=$(awk "BEGIN { print ${ERR_AFTER:-1} * 100 }")
  echo "  t+${i}0s payments error rate = $(printf %.2f "$AFTER_PCT")%"
  # Healthy baseline is ~2%; consider <5% recovered.
  if awk "BEGIN { exit !(${ERR_AFTER:-1} < 0.05) }"; then
    echo "  → recovered."
    break
  fi
  sleep 10
done

echo
echo "Done. Try:"
echo "  open http://localhost:3000                                 # Grafana dashboard"
echo "  open http://localhost:9090                                 # Prometheus"
echo "  curl ${BASE}/v1/runs/${REM_ID} | jq                        # full run snapshot"
echo "  curl http://localhost:7778/v1/signals?scope_id=${SCOPE_ID} | jq  # raw chronos signals"
echo "  ${COMPOSE} --profile demo down -v                          # tear down + wipe volumes"
