#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KUBE_CONTEXT="${KUBE_CONTEXT:-local}"
NS="${NS:-zeta-poc}"
PORT="${GIT_PORT:-19418}"

kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT/manifests/git-server.yaml"
kubectl --context "$KUBE_CONTEXT" -n "$NS" rollout status deploy/git-server --timeout=120s

pf_pid=""
cleanup() {
  if [ -n "$pf_pid" ]; then
    kill "$pf_pid" 2>/dev/null || true
    wait "$pf_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT

kubectl --context "$KUBE_CONTEXT" -n "$NS" port-forward svc/git-server "$PORT:9418" \
  >/tmp/zeta-git-portforward.log 2>&1 &
pf_pid=$!

ready=0
for _ in $(seq 1 40); do
  if git ls-remote "git://127.0.0.1:$PORT/cpp.git" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.5
done
if [ "$ready" -ne 1 ]; then
  echo "git server did not become ready; see /tmp/zeta-git-portforward.log" >&2
  exit 1
fi

push_example() {
  local src="$1"
  local repo="$2"
  local tmp
  tmp="$(mktemp -d)"
  cp -a "$ROOT/examples/$src/." "$tmp/"
  git -C "$tmp" init -q
  git -C "$tmp" checkout -q -b main
  git -C "$tmp" add -A
  git -C "$tmp" -c user.name="Zeta POC" -c user.email="poc@zeta.local" \
    commit -qm "poc snapshot"
  git -C "$tmp" push --force "git://127.0.0.1:$PORT/$repo.git" main
  git -C "$tmp" rev-parse HEAD
  rm -rf "$tmp"
}

cpp_sha="$(push_example cpp cpp)"
python_sha="$(push_example python-uv python-uv)"
go_sha="$(push_example go go)"

echo "cpp commit:       $cpp_sha"
echo "python-uv commit: $python_sha"
echo "go commit:        $go_sha"
