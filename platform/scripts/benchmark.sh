#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

KUBE_CONTEXT="${KUBE_CONTEXT:-local}"
NS="${NS:-zeta-poc}"
KUBECTL=(kubectl --context "$KUBE_CONTEXT" -n "$NS")

"${KUBECTL[@]}" delete job poc-cpp poc-python --ignore-not-found --wait=true
"${KUBECTL[@]}" delete pvc zeta-poc-cache --ignore-not-found --wait=true
"${KUBECTL[@]}" apply -f manifests/cache-pvc.yaml

for target in cpp python; do
  for run in 1 2; do
    log="/tmp/zeta-$target-$run.log"
    echo "=== $target run $run ==="
    start="$(date +%s.%N)"
    ./scripts/run-build.sh "$target" >"$log" 2>&1
    end="$(date +%s.%N)"
    awk -v s="$start" -v e="$end" 'BEGIN { printf "wall=%.2fs\n", e-s }'
    if [ "$target" = cpp ]; then
      grep -E 'Cacheable calls|Hits:|Misses:|Cache size' "$log" | tail -6 || true
    else
      grep -E 'Prepared|Installed|Audited|/cache/uv' "$log" | tail -6 || true
    fi
  done
done
