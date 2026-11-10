#!/usr/bin/env bash
set -euo pipefail

target="${1:?usage: run-build.sh <cpp|python|go> [git|configmap]}"
mode="${2:-${CONTEXT_MODE:-configmap}}"
case "$target" in
  cpp) job=poc-cpp; dir=cpp ;;
  python) job=poc-python; dir=python-uv ;;
  go) job=poc-go; dir=go ;;
  *) echo "unknown target: $target" >&2; exit 1 ;;
esac
case "$mode" in
  git) job="$job-git"; manifest="manifests/job-$target-git.yaml" ;;
  configmap) manifest="manifests/job-$target.yaml" ;;
  *) echo "unknown context mode: $mode" >&2; exit 1 ;;
esac

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

KUBE_CONTEXT="${KUBE_CONTEXT:-local}"
NS="${NS:-zeta-poc}"
KUBECTL=(kubectl --context "$KUBE_CONTEXT" -n "$NS")

"${KUBECTL[@]}" apply -f manifests/namespace.yaml
"${KUBECTL[@]}" apply -f manifests/pvc.yaml
"${KUBECTL[@]}" apply -f manifests/cache-pvc.yaml

if [ "$mode" = configmap ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  tar -C "examples/$dir" -czf "$tmp/context.tar.gz" .
  "${KUBECTL[@]}" create configmap "ctx-$target" \
    --from-file=context.tar.gz="$tmp/context.tar.gz" \
    --dry-run=client -o yaml | "${KUBECTL[@]}" apply -f -
else
  if ! "${KUBECTL[@]}" get deploy git-server >/dev/null 2>&1; then
    echo "git server not found; run scripts/init-git.sh first" >&2
    exit 1
  fi
fi

"${KUBECTL[@]}" delete job "$job" --ignore-not-found --wait=true
"${KUBECTL[@]}" apply -f "$manifest"

if ! "${KUBECTL[@]}" wait --for=condition=complete "job/$job" --timeout=600s; then
  "${KUBECTL[@]}" logs "job/$job" --all-containers=true || true
  exit 1
fi

"${KUBECTL[@]}" logs "job/$job" --all-containers=true

"${KUBECTL[@]}" delete pod artifact-server --ignore-not-found --wait=true --grace-period=0 --force
"${KUBECTL[@]}" apply -f manifests/artifact-pod.yaml
"${KUBECTL[@]}" wait --for=condition=Ready pod/artifact-server --timeout=120s

mkdir -p "out/$target"
"${KUBECTL[@]}" exec artifact-server -- tar -C "/artifacts/$target" -cf - . | tar -C "out/$target" -xf -

"${KUBECTL[@]}" delete pod artifact-server --ignore-not-found --wait=true --grace-period=0 --force
