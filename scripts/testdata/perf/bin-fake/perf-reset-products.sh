#!/usr/bin/env bash
# Stub of scripts/perf-reset-products.sh --print-sql for the offline suite: echoes
# its arguments into a recognisable SQL comment so the test can check the
# ConfigMap carries the generator's output verbatim.
set -euo pipefail
[[ "${1:-}" == --print-sql ]] || { echo "stub: expected --print-sql" >&2; exit 2; }
shift
echo "-- perf-reset stub: $*"
echo "SELECT 'COUNT products=250106 seed=250000 outbox_pending=0 last_analyze=null';"
