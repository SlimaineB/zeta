#!/usr/bin/env bash
set -euo pipefail

KUBE_CONTEXT="${KUBE_CONTEXT:-local}"
ok=0

pass() { printf '  ok    %s\n' "$1"; }
warn() { printf '  warn  %s\n' "$1"; }
fail() { printf '  fail  %s\n' "$1"; ok=1; }

echo "Prérequis Zeta (contexte kubectl: $KUBE_CONTEXT)"

for cmd in kubectl docker sudo make go git; do
  if command -v "$cmd" >/dev/null 2>&1; then
    pass "$cmd"
  else
    fail "$cmd introuvable"
  fi
done

if command -v kubectl >/dev/null 2>&1; then
  if kubectl --context "$KUBE_CONTEXT" get nodes >/dev/null 2>&1; then
    pass "contexte kubectl '$KUBE_CONTEXT' accessible"
  else
    fail "contexte kubectl '$KUBE_CONTEXT' inaccessible"
  fi
fi

if command -v docker >/dev/null 2>&1; then
  if docker info >/dev/null 2>&1; then
    pass "démon Docker accessible"
  else
    fail "démon Docker inaccessible"
  fi
fi

if command -v sudo >/dev/null 2>&1; then
  if sudo -n true >/dev/null 2>&1; then
    pass "sudo sans mot de passe (import containerd)"
  else
    warn "sudo -n indisponible; l’import k3s demandera un mot de passe"
  fi
fi

if command -v kubectl >/dev/null 2>&1 && kubectl --context "$KUBE_CONTEXT" get nodes >/dev/null 2>&1; then
  if kubectl --context "$KUBE_CONTEXT" -n zeta-poc get ingress zeta-portal >/dev/null 2>&1; then
    pass "plateforme déjà déployée"
  else
    warn "plateforme pas encore déployée"
  fi
fi

exit "$ok"
