#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KUBE_CONTEXT="${KUBE_CONTEXT:-local}"
IMAGE="${IMAGE:-zeta/portal:0.1}"
WITH_GIT_SERVER="${WITH_GIT_SERVER:-0}"
NAMESPACE="zeta-poc"
HOST="zeta.local"

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "erreur: $1 est requis" >&2
    exit 1
  fi
}

need kubectl
need docker
need sudo
need make

if ! kubectl --context "$KUBE_CONTEXT" get nodes >/dev/null 2>&1; then
  echo "erreur: contexte kubectl '$KUBE_CONTEXT' inaccessible" >&2
  echo "        démarrez k3s ou utilisez KUBE_CONTEXT=..." >&2
  exit 1
fi
if ! sudo -n true >/dev/null 2>&1; then
  echo "erreur: sudo sans mot de passe requis pour importer les images dans k3s" >&2
  echo "        configurez sudoers ou importez les images manuellement" >&2
  exit 1
fi

echo "==> Images de build"
"$ROOT/platform/scripts/build-images.sh"

echo "==> Portail + syncd"
make -C "$ROOT/portal" image IMAGE="$IMAGE"
docker save "$IMAGE" | sudo -n k3s ctr images import -

echo "==> Manifests"
kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT/platform/manifests/namespace.yaml"
kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT/platform/manifests/pvc.yaml"
kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT/platform/manifests/cache-pvc.yaml"
kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT/platform/manifests/context-pvc.yaml"
kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT/platform/manifests/portal.yaml"

if [ "$WITH_GIT_SERVER" = "1" ]; then
  kubectl --context "$KUBE_CONTEXT" apply -f "$ROOT/platform/manifests/git-server.yaml"
  kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" rollout status deploy/git-server --timeout=180s
fi

echo "==> Rollout du portail"
kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" rollout status deploy/zeta-portal --timeout=180s

addr="$(kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" get ingress zeta-portal -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || true)"
if [ -z "$addr" ]; then
  addr="$(kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" get ingress zeta-portal -o jsonpath='{.status.loadBalancer.ingress[0].hostname}' 2>/dev/null || true)"
fi

cat <<MSG

Plateforme installée.

  Portail : http://$HOST
  Ingress : ${addr:-<adresse inconnue>}

Sur la machine utilisateur, ajoutez si nécessaire :

  ${addr:-<adresse>} $HOST

Puis installez le client :

  make install-user

Et connectez-vous :

  zeta login --server http://$HOST --email dev@example.com --password dev
MSG

if [ "$WITH_GIT_SERVER" = "1" ]; then
  cat <<MSG

Serveur Git d’exemple déployé. Pour pousser les exemples :

  make -C platform git-init
MSG
elif ! kubectl --context "$KUBE_CONTEXT" -n "$NAMESPACE" get deploy/git-server >/dev/null 2>&1; then
  cat <<MSG

Serveur Git d’exemple non déployé. Pour l’activer :

  make install-platform WITH_GIT_SERVER=1
  make -C platform git-init
MSG
fi
