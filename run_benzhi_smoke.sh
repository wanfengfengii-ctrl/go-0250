#!/usr/bin/env bash
# WindowProof smoke test: builds the binary, starts the service against a
# temporary SQLite database, probes its local health and API behavior, then
# cleans up every process and temporary file. Deterministic and offline.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PORT="${WP_PORT:-18080}"
BASE="http://127.0.0.1:${PORT}"
WORKDIR_TMP="$(mktemp -d)"
DB_PATH="${WORKDIR_TMP}/windowproof-smoke.db"
BIN="${WORKDIR_TMP}/windowproof"
SERVER_PID=""

cleanup() {
  if [[ -n "${SERVER_PID}" ]] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill "${SERVER_PID}" 2>/dev/null || true
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  rm -rf "${WORKDIR_TMP}"
}
trap cleanup EXIT

echo "building binary..."
(cd "${ROOT}" && go build -o "${BIN}" ./cmd/windowproof)

echo "starting service on port ${PORT}..."
"${BIN}" -addr "127.0.0.1:${PORT}" -db "${DB_PATH}" &
SERVER_PID=$!

# Wait for the health endpoint to become ready.
ready=""
for _ in $(seq 1 50); do
  if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
    echo "server exited during startup" >&2
    exit 1
  fi
  if health="$(curl -sS "http://127.0.0.1:${PORT}/healthz" 2>/dev/null)"; then
    ready="${health}"
    break
  fi
  sleep 0.1
done

if [[ -z "${ready}" ]]; then
  echo "service did not become ready" >&2
  exit 1
fi
echo "health response: ${ready}"

# Assert the health body (captured in a variable, never piped through grep).
if [[ "${ready}" != *'"status":"ok"'* ]]; then
  echo "unexpected health body: ${ready}" >&2
  exit 1
fi

# Create a task.
create_body="$(curl -sS -X POST "${BASE}/api/v1/tasks" \
  -H 'Content-Type: application/json' \
  -d '{"task_id":"smoke-1","operation_id":"smoke-1-create"}')"
if [[ "${create_body}" != *'"state":"pending_lock"'* ]]; then
  echo "create task failed: ${create_body}" >&2
  exit 1
fi
echo "created task: ${create_body}"

# Lock the task against the demo catalogue.
lock_body="$(curl -sS -X POST "${BASE}/api/v1/tasks/smoke-1/lock" \
  -H 'Content-Type: application/json' \
  -d '{"operation_id":"smoke-1-lock","expected_revision":0,"catalog_revision_id":"rev-demo","window_unit_id":"W-E-01","profile_batch":"p1","glass_batch":"g1","seal_batch":"s1","plan_id":"plan-1","chamber_id":"chamber-1","measurement_point_ids":["mp-1","mp-2"]}')"
if [[ "${lock_body}" != *'"state":"installing"'* ]]; then
  echo "lock task failed: ${lock_body}" >&2
  exit 1
fi
echo "locked task: ${lock_body}"

# Fetch the task and assert the locked snapshot is present.
get_body="$(curl -sS "${BASE}/api/v1/tasks/smoke-1")"
if [[ "${get_body}" != *'"locked_snapshot_hash"'* ]]; then
  echo "get task failed: ${get_body}" >&2
  exit 1
fi
echo "get task ok (snapshot hash present)"

echo "SMOKE OK"
