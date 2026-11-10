#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-local}"
NAMESPACE="zeta-poc"

if [ "${FORCE:-0}" != "1" ]; then
  read -r -p "Supprimer le namespace $NAMESPACE et tous les builds/PVC ? [y/N] " answer
  case "$answer" in
    y|Y|yes|YES) ;;
    *) echo "annulé"; exit 0 ;;
  esac
fi

kubectl --context "$KUBE_CONTEXT" delete namespace "$NAMESPACE" --ignore-not-found
echo "namespace $NAMESPACE supprimé"
echo "les images zeta/* restent importées dans k3s"
