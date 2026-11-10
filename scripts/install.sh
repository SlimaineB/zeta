#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RELEASE="${ZETA_RELEASE:-zeta}"
NAMESPACE="${ZETA_NAMESPACE:-zeta-poc}"
CONTEXT="${KUBE_CONTEXT:-local}"
HOST="${ZETA_HOST:-zeta.local}"
REGISTRY="${ZETA_REGISTRY:-}"
TAG="${ZETA_TAG:-}"
CHART="${ZETA_CHART:-$ROOT/charts/zeta}"
CHART_VERSION="${ZETA_CHART_VERSION:-0.1.0}"
STORAGE_CLASS="${ZETA_STORAGE_CLASS:-}"
AUTH_SECRET="${ZETA_AUTH_SECRET:-}"
FROM_REGISTRY=0
WITH_GIT_SERVER=0
INGRESS=1
ADOPT=0
DRY_RUN=0
AUTH_DEV=1
OIDC_ISSUER=""
OIDC_CLIENT_ID="zeta-portal"
OIDC_CLIENT_SECRET=""
OIDC_REDIRECT_URL=""
SECURE_COOKIE=0

usage() {
  cat <<'EOF'
Install or upgrade Zeta with Helm.

Usage: ./scripts/install.sh [options]

Modes:
  (default)                 Build images locally, import them into k3s, then Helm install
  --registry REGISTRY       Use images from a registry (for example ghcr.io/owner)
  --from-registry           Install the published OCI chart from REGISTRY/charts/zeta
  --local                   Force local image mode

Options:
  --namespace NAME          Namespace (default: zeta-poc)
  --context NAME            kubectl context (default: local)
  --host HOST               Ingress host (default: zeta.local)
  --tag TAG                 Image tag (local default: 0.1, remote default: chart version)
  --chart PATH              Local chart path (default: charts/zeta)
  --chart-version VERSION   Published chart version (default: 0.1.0)
  --storage-class CLASS     StorageClass for shared PVCs (default: local-path)
  --with-git-server         Deploy the optional example Git server
  --no-ingress              Disable the Ingress
  --adopt                   Adopt existing non-Helm Zeta resources in the namespace
  --oidc-issuer URL         Enable OIDC instead of development auth
  --oidc-client-id ID       OIDC client ID (default: zeta-portal)
  --oidc-client-secret S    OIDC client secret
  --oidc-redirect-url URL   OIDC callback URL
  --auth-secret NAME        Use an existing Secret with session-key and oidc-client-secret
  --secure-cookie           Set Secure cookies (HTTPS)
  --dry-run                 Print the Helm command without changing the cluster
  -h, --help                Show this help

Environment:
  ZETA_REGISTRY, ZETA_TAG, ZETA_NAMESPACE, ZETA_HOST, ZETA_CHART_VERSION
  KUBE_CONTEXT, GHCR_TOKEN, GHCR_USER
EOF
}

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: $1 is required" >&2
    exit 1
  fi
}

while [ $# -gt 0 ]; do
  case "$1" in
    --namespace) NAMESPACE="$2"; shift 2 ;;
    --context) CONTEXT="$2"; shift 2 ;;
    --host) HOST="$2"; shift 2 ;;
    --registry) REGISTRY="$2"; shift 2 ;;
    --tag) TAG="$2"; shift 2 ;;
    --chart) CHART="$2"; shift 2 ;;
    --chart-version) CHART_VERSION="$2"; shift 2 ;;
    --storage-class) STORAGE_CLASS="$2"; shift 2 ;;
    --auth-secret) AUTH_SECRET="$2"; shift 2 ;;
    --oidc-issuer) AUTH_DEV=0; OIDC_ISSUER="$2"; shift 2 ;;
    --oidc-client-id) OIDC_CLIENT_ID="$2"; shift 2 ;;
    --oidc-client-secret) OIDC_CLIENT_SECRET="$2"; shift 2 ;;
    --oidc-redirect-url) OIDC_REDIRECT_URL="$2"; shift 2 ;;
    --from-registry) FROM_REGISTRY=1; shift ;;
    --with-git-server) WITH_GIT_SERVER=1; shift ;;
    --no-ingress) INGRESS=0; shift ;;
    --adopt) ADOPT=1; shift ;;
    --secure-cookie) SECURE_COOKIE=1; shift ;;
    --local) REGISTRY=""; FROM_REGISTRY=0; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *)
      echo "error: unknown option: $1" >&2
      usage >&2
      exit 1
      ;;
  esac
done

need helm
need kubectl

if [ "$DRY_RUN" != "1" ]; then
  if ! kubectl --context "$CONTEXT" get nodes >/dev/null 2>&1; then
    if command -v k3s >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
      echo "==> Starting k3s"
      sudo -n bash -c 'nohup k3s server >/tmp/k3s-server.log 2>&1 &'
      for _ in $(seq 1 60); do
        kubectl --context "$CONTEXT" get nodes >/dev/null 2>&1 && break
        sleep 2
      done
    fi
  fi

  if ! kubectl --context "$CONTEXT" get nodes >/dev/null 2>&1; then
    echo "error: kubectl context '$CONTEXT' is not reachable" >&2
    echo "       start k3s or pass --context NAME" >&2
    exit 1
  fi

  if helm status "$RELEASE" --kube-context "$CONTEXT" --namespace "$NAMESPACE" >/dev/null 2>&1; then
    status="$(helm status "$RELEASE" --kube-context "$CONTEXT" --namespace "$NAMESPACE" -o json 2>/dev/null | tr -d '[:space:]' | sed -n 's/.*"status":"\([^"]*\)".*/\1/p')"
    case "$status" in
      failed|pending-install|pending-upgrade|pending-rollback)
        echo "==> Removing failed Helm release $RELEASE"
        helm uninstall "$RELEASE" --kube-context "$CONTEXT" --namespace "$NAMESPACE" --wait
        ;;
    esac
  fi
fi

if [ -n "$REGISTRY" ]; then
  TAG="${TAG:-$CHART_VERSION}"
else
  TAG="${TAG:-0.1}"
fi

helm_args=(--kube-context "$CONTEXT" upgrade --install "$RELEASE" --namespace "$NAMESPACE" --create-namespace)

if [ "$FROM_REGISTRY" = "1" ]; then
  if [ -z "$REGISTRY" ]; then
    echo "error: --from-registry requires --registry" >&2
    exit 1
  fi
  helm_args+=("oci://${REGISTRY}/charts/zeta" --version "$CHART_VERSION")
else
  helm_args+=("$CHART")
fi

if [ "$INGRESS" = "1" ]; then
  helm_args+=(--set ingress.enabled=true --set ingress.host="$HOST")
else
  helm_args+=(--set ingress.enabled=false)
fi

if [ "$WITH_GIT_SERVER" = "1" ]; then
  helm_args+=(--set gitServer.enabled=true)
fi

if [ -n "$STORAGE_CLASS" ]; then
  helm_args+=(--set portal.storageClass="$STORAGE_CLASS")
fi

if [ -n "$AUTH_SECRET" ]; then
  helm_args+=(--set auth.existingSecret="$AUTH_SECRET")
elif [ "$AUTH_DEV" = "1" ]; then
  helm_args+=(--set auth.dev=true)
else
  if [ -z "$OIDC_ISSUER" ]; then
    echo "error: --oidc-issuer is required when development auth is disabled" >&2
    exit 1
  fi
  helm_args+=(--set auth.dev=false --set auth.oidc.issuer="$OIDC_ISSUER" --set auth.oidc.clientID="$OIDC_CLIENT_ID")
  if [ -n "$OIDC_CLIENT_SECRET" ]; then
    helm_args+=(--set auth.oidc.clientSecret="$OIDC_CLIENT_SECRET")
  fi
  if [ -n "$OIDC_REDIRECT_URL" ]; then
    helm_args+=(--set auth.oidc.redirectURL="$OIDC_REDIRECT_URL")
  fi
fi

if [ "$SECURE_COOKIE" = "1" ]; then
  helm_args+=(--set auth.secureCookie=true)
fi

if [ -n "$REGISTRY" ]; then
  helm_args+=(--set image.registry="$REGISTRY")
  helm_args+=(--set image.tag="$TAG")

  if [ -n "${GHCR_TOKEN:-}" ]; then
    kubectl --context "$CONTEXT" get namespace "$NAMESPACE" >/dev/null 2>&1 \
      || kubectl --context "$CONTEXT" create namespace "$NAMESPACE" >/dev/null
    kubectl --context "$CONTEXT" -n "$NAMESPACE" create secret docker-registry zeta-pull \
      --docker-server="${REGISTRY%%/*}" \
      --docker-username="${GHCR_USER:-$USER}" \
      --docker-password="$GHCR_TOKEN" \
      --dry-run=client -o yaml \
      | kubectl --context "$CONTEXT" apply -f - >/dev/null
    kubectl --context "$CONTEXT" -n "$NAMESPACE" patch serviceaccount default \
      --type merge -p '{"imagePullSecrets":[{"name":"zeta-pull"}]}' >/dev/null
    helm_args+=(--set image.pullSecrets[0].name=zeta-pull)
  fi
else
  if [ "$DRY_RUN" != "1" ]; then
    need docker
    need make
    need sudo
    if ! sudo -n true >/dev/null 2>&1; then
      echo "error: passwordless sudo is required to import images into k3s" >&2
      exit 1
    fi
  fi
  helm_args+=(--set image.repository=zeta/portal --set image.tag="$TAG")
fi

if [ "$DRY_RUN" = "1" ]; then
  printf 'helm'
  printf ' %q' "${helm_args[@]}"
  printf '\n'
  exit 0
fi

if ! helm status "$RELEASE" --kube-context "$CONTEXT" --namespace "$NAMESPACE" >/dev/null 2>&1; then
  if [ "$ADOPT" = "1" ]; then
    echo "==> Adopting existing Zeta resources into Helm"
    for resource in \
      serviceaccount/zeta-portal \
      role/zeta-portal \
      rolebinding/zeta-portal \
      service/zeta-portal \
      ingress/zeta-portal \
      pvc/zeta-poc-artifacts \
      pvc/zeta-poc-cache \
      pvc/zeta-poc-contexts; do
      if kubectl --context "$CONTEXT" -n "$NAMESPACE" get "$resource" >/dev/null 2>&1; then
        kubectl --context "$CONTEXT" -n "$NAMESPACE" label "$resource" app.kubernetes.io/managed-by=Helm --overwrite >/dev/null
        kubectl --context "$CONTEXT" -n "$NAMESPACE" annotate "$resource" \
          meta.helm.sh/release-name="$RELEASE" \
          meta.helm.sh/release-namespace="$NAMESPACE" --overwrite >/dev/null
      fi
    done
    if kubectl --context "$CONTEXT" -n "$NAMESPACE" get deployment/zeta-portal >/dev/null 2>&1; then
      echo "==> Replacing the raw portal Deployment (selector is immutable)"
      kubectl --context "$CONTEXT" -n "$NAMESPACE" delete deployment/zeta-portal --wait=true >/dev/null
    fi
    if [ "$WITH_GIT_SERVER" = "1" ]; then
      kubectl --context "$CONTEXT" -n "$NAMESPACE" delete deployment/git-server service/git-server --ignore-not-found >/dev/null
      if kubectl --context "$CONTEXT" -n "$NAMESPACE" get pvc/zeta-poc-git >/dev/null 2>&1; then
        kubectl --context "$CONTEXT" -n "$NAMESPACE" label pvc/zeta-poc-git app.kubernetes.io/managed-by=Helm --overwrite >/dev/null
        kubectl --context "$CONTEXT" -n "$NAMESPACE" annotate pvc/zeta-poc-git \
          meta.helm.sh/release-name="$RELEASE" \
          meta.helm.sh/release-namespace="$NAMESPACE" --overwrite >/dev/null
      fi
    fi
  elif kubectl --context "$CONTEXT" -n "$NAMESPACE" get deployment/zeta-portal >/dev/null 2>&1; then
    echo "error: existing non-Helm Zeta resources found in namespace '$NAMESPACE'" >&2
    echo "       run with --adopt to migrate them, or run 'make uninstall-platform' first" >&2
    exit 1
  fi
fi

if [ -z "$REGISTRY" ]; then
  echo "==> Build and import build images (tag $TAG)"
  TAG="$TAG" "$ROOT/platform/scripts/build-images.sh"
  echo "==> Build and import portal image"
  make -C "$ROOT/portal" import IMAGE="zeta/portal:$TAG"
fi

echo "==> Helm install/upgrade"
helm "${helm_args[@]}"

echo "==> Rollout"
kubectl --context "$CONTEXT" -n "$NAMESPACE" rollout status "deploy/zeta-portal" --timeout=180s

if [ "$INGRESS" = "1" ]; then
  LOGIN_URL="http://$HOST"
  echo
  echo "Zeta is available at $LOGIN_URL"
  echo "Add the ingress address to your hosts file if needed:"
  echo "  kubectl --context $CONTEXT -n $NAMESPACE get ingress zeta-portal"
else
  LOGIN_URL="http://localhost:8080"
  echo
  echo "Zeta is installed. Port-forward with:"
  echo "  kubectl --context $CONTEXT -n $NAMESPACE port-forward svc/zeta-portal 8080:80"
fi

if [ "$AUTH_DEV" = "1" ] && [ -z "$AUTH_SECRET" ]; then
  echo
  echo "Log in with the CLI:"
  echo "  zeta login --server $LOGIN_URL --email dev@example.com --password dev"
fi
