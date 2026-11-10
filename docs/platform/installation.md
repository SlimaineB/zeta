# Install the platform

This page installs the Zeta platform with Helm: the portal, RBAC, shared PVCs,
and optionally the example Git server. Developers install the CLI separately;
see [Install the CLI](../developer/installation.md).

## Prerequisites

| Requirement | Notes |
|---|---|
| Kubernetes 1.27+ | Tested on a single-node k3s cluster. |
| Helm 3.14+ | OCI chart support is required. |
| Ingress controller | Traefik is the default on k3s; use `ingress.className=nginx` otherwise. |
| StorageClass | `local-path` works on a single node. Use an EBS/gp3 class for durability and volume expansion. |
| `kubectl` | Access to the target cluster. |
| Docker, k3s, and passwordless `sudo` | Only for the source/local image build. |

The published images are public. If the GHCR packages are private, see
[Private registry](#private-registry).

## Install the published chart

```bash
helm upgrade --install zeta oci://ghcr.io/slimaineb/charts/zeta \
  --version 0.1.0 \
  --namespace zeta-poc --create-namespace \
  --set ingress.host=zeta.local
```

The chart defaults to:

- `ingress.enabled=true` with the `traefik` class and host `zeta.local`;
- development authentication (`dev@example.com` / `dev`);
- `local-path` storage;
- no Git server.

Adjust these values for your cluster:

```bash
helm upgrade --install zeta oci://ghcr.io/slimaineb/charts/zeta \
  --version 0.1.0 \
  --namespace zeta-poc --create-namespace \
  --set ingress.className=nginx \
  --set ingress.host=zeta.example.com \
  --set portal.storageClass=gp3 \
  --set auth.dev=false \
  --set auth.oidc.issuer=https://idp.example.com/realms/dev \
  --set auth.oidc.clientID=zeta-portal \
  --set auth.oidc.clientSecret='<secret>' \
  --set auth.oidc.redirectURL=https://zeta.example.com/oidc/callback \
  --set auth.secureCookie=true
```

For a production session key, use a Kubernetes Secret instead of putting it in
the command line. See [Helm chart reference](helm-chart.md#authentication).

## Private registry

`helm registry login` authenticates Helm to pull the chart. Kubernetes also needs
credentials to pull the images:

```bash
echo "$GHCR_TOKEN" | helm registry login ghcr.io -u "$GHCR_USER" --password-stdin

kubectl create namespace zeta-poc --dry-run=client -o yaml | kubectl apply -f -
kubectl -n zeta-poc create secret docker-registry zeta-pull \
  --docker-server=ghcr.io \
  --docker-username="$GHCR_USER" \
  --docker-password="$GHCR_TOKEN"

helm upgrade --install zeta oci://ghcr.io/slimaineb/charts/zeta \
  --version 0.1.0 \
  --namespace zeta-poc --create-namespace \
  --set image.pullSecrets[0].name=zeta-pull
```

The chart applies `image.pullSecrets` to the portal and the optional Git server.
Build Jobs use the namespace's default ServiceAccount, so add the same secret
there:

```bash
kubectl -n zeta-poc patch serviceaccount default -p \
  '{"imagePullSecrets":[{"name":"zeta-pull"}]}'
```

The `scripts/install.sh --registry ghcr.io/<owner>` path can create the pull
secret automatically when `GHCR_TOKEN` is set.

## Install from source

Use this path on a local k3s cluster when you want to build images from the
working tree:

```bash
./scripts/install.sh
```

The installer:

1. checks `kubectl` and the context (default `local`);
2. builds the language, Git server, and portal images;
3. imports them into k3s containerd;
4. runs `helm upgrade --install` with the local chart;
5. waits for the portal rollout.

Useful options:

```bash
./scripts/install.sh --with-git-server
./scripts/install.sh --namespace zeta-dev --context local
./scripts/install.sh --adopt
./scripts/install.sh --dry-run
./scripts/install.sh --help
```

`--adopt` migrates an existing raw manifest installation into Helm. It keeps the
PVCs and replaces the raw portal Deployment, so expect a short rollout while the
new Helm-managed pod starts. Without `--adopt`, the installer stops if it finds a
non-Helm `zeta-portal` Deployment in the namespace. You can also remove the old
installation first:

```bash
make uninstall-platform
```

!!! warning "PVC data"
    `make uninstall-platform` deletes the namespace and its PVCs, including
    caches, artifacts, and environment workspaces. Use `--adopt` to keep them.

## Verify

```bash
helm status zeta -n zeta-poc
kubectl -n zeta-poc get deploy,svc,ingress,pods,pvc
curl -fsS http://zeta.local/healthz
```

`curl` returns `ok`. The portal logs should show the listening address and the
namespace:

```bash
kubectl -n zeta-poc logs deploy/zeta-portal --tail=20
```

## Access the portal

### Ingress

```bash
kubectl -n zeta-poc get ingress zeta-portal
```

Add the returned address to the hosts file of the machine that opens the
browser, and to `/etc/hosts` in WSL if you use it:

```text
<ADDRESS> zeta.local
```

### Port-forward

```bash
kubectl -n zeta-poc port-forward --address 0.0.0.0 svc/zeta-portal 8080:80
```

Open `http://localhost:8080`. `--address 0.0.0.0` is required when the browser
runs on Windows and the cluster runs in WSL.

### Development credentials

| Email | Password | Groups |
|---|---|---|
| `dev@example.com` | `dev` | `devs` |
| `admin@example.com` | `admin` | `admins` |
| `alice@example.com` | `alice` | `backend` |

```bash
zeta login --server http://zeta.local --email dev@example.com --password dev
```

## Upgrade

```bash
helm upgrade --install zeta oci://ghcr.io/slimaineb/charts/zeta \
  --version 0.2.0 \
  --namespace zeta-poc \
  --reuse-values
```

`--reuse-values` keeps your previous overrides. To review the new defaults
first:

```bash
helm show values oci://ghcr.io/slimaineb/charts/zeta --version 0.2.0
```

The chart keeps PVCs across upgrades. The portal image tag follows the chart
`appVersion`; helper images (init, syncd, Git server) use the same registry and
tag.

## Uninstall

```bash
helm uninstall zeta -n zeta-poc
```

The shared PVCs have `helm.sh/resource-policy: keep`, so Helm leaves them in
place. To remove everything, including caches, artifacts, and environment
workspaces:

```bash
kubectl delete namespace zeta-poc
```

Imported images remain in containerd on a k3s node.

## Next steps

- [Helm chart reference](helm-chart.md)
- [Deployment and operations](../operations/deployment.md)
- [Security](../operations/security.md)
- [Troubleshooting](../operations/troubleshooting.md)
