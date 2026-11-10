# Deployment and operations

This page covers day-two operations: upgrades, logs, cleanup, releases, and
documentation publishing.

## Update the platform with Helm

The recommended upgrade path is the published chart:

```bash
helm upgrade --install zeta oci://ghcr.io/slimaineb/charts/zeta \
  --version 0.2.0 \
  --namespace zeta-poc \
  --reuse-values
```

Review the new defaults before upgrading:

```bash
helm show values oci://ghcr.io/slimaineb/charts/zeta --version 0.2.0
```

`--reuse-values` keeps your previous overrides. The chart updates the portal,
RBAC, and optional Git server; shared PVCs are preserved. The portal image and
helper images (context init, `syncd`, Git server) use the chart version by
default.

See the [Helm chart reference](../platform/helm-chart.md) for values and
examples.

## Deploy from source

On a local k3s cluster, build the images and install the local chart:

```bash
./scripts/install.sh
```

Or use the Make target:

```bash
make install-smart
```

The raw manifest path remains available for development:

```bash
cd portal
make test
make deploy
```

`make deploy` compiles the portal, builds `zeta/portal:0.1`, imports it into
k3s, applies `platform/manifests/portal.yaml`, and waits for the rollout. From
the repository root, `make install-platform` rebuilds the images and reapplies
the raw manifests.

## Build and import images

```bash
cd platform
make images
```

This builds the language images and the Git server image, then imports them into
k3s containerd. To re-import a single image:

```bash
docker save zeta/go:0.1 | sudo -n k3s ctr images import -
```

## Install or update the CLI

Users should install the release binary:

```bash
curl -fsSL https://raw.githubusercontent.com/SlimaineB/zeta/main/scripts/install-cli.sh | sh
```

On Windows:

```powershell
irm https://raw.githubusercontent.com/SlimaineB/zeta/main/scripts/install-cli.ps1 | iex
```

For a source build:

```bash
make install-user
```

Or manually:

```bash
cd cli
make build
sudo install -m 0755 bin/zeta /usr/local/bin/zeta
```

See [Install the CLI](../developer/installation.md).

## Release pipeline

The [release workflow](https://github.com/SlimaineB/zeta/blob/main/.github/workflows/release.yml)
runs on `v*` tags. For `v0.1.0` it publishes:

- images `ghcr.io/<owner>/zeta/{portal,cpp,go,python-uv,git-server}:0.1.0`
  and `:latest`;
- CLI assets `zeta-{linux,darwin,windows}-{amd64,arm64}`;
- the Helm chart at `oci://ghcr.io/<owner>/charts/zeta:0.1.0`;
- a GitHub Release with the binaries, chart, and `checksums.txt`.

The chart package is patched with the release registry, so a plain
`helm install` uses the published images. The workflow also attempts to make the
GHCR packages public. If a package stays private, follow the
[private registry](../platform/installation.md#private-registry) steps.

## Status

```bash
make status
helm status zeta -n zeta-poc
```

Useful checks:

```bash
kubectl --context local -n zeta-poc get deploy,svc,ingress,pods,pvc
kubectl --context local -n zeta-poc get jobs
kubectl --context local -n zeta-poc get events --sort-by=.lastTimestamp
```

## Logs

```bash
kubectl --context local -n zeta-poc logs deploy/zeta-portal -f
```

Or:

```bash
cd portal
make logs
```

Environment sync logs:

```bash
kubectl --context local -n zeta-poc logs deploy/zeta-env-<id> -c syncd
```

## Restart k3s under WSL

```bash
sudo -n bash -c 'nohup k3s server >/tmp/k3s-server.log 2>&1 &'
kubectl --context local get nodes
```

## Clean up builds

Jobs have a 1-hour TTL. Manual deletion:

```bash
kubectl --context local -n zeta-poc delete job \
  -l zeta.io/managed-by=portal
```

## Reset the cache

!!! warning "Shared cache"
    This removes `ccache`, `uv`, and Go caches for every build using the shared
    PVC.

```bash
zeta volumes --role cache
kubectl --context local -n zeta-poc delete job \
  -l zeta.io/managed-by=portal --ignore-not-found
kubectl --context local -n zeta-poc delete pvc zeta-poc-cache
```

The PVC is recreated on the next build. Profiles with a dedicated `cachePVC`
keep their own volume.

## Reset artifacts

```bash
zeta volumes --role artifacts
kubectl --context local -n zeta-poc delete pvc zeta-poc-artifacts
```

The shared PVC is recreated on the next build.

## Reset an environment

```bash
zeta deactivate --name dev      # keep the workspace
zeta rm dev                     # delete the workspace and secret
```

If `rm` reports that the volume is still terminating, wait for the finalizer and
retry.

## Uninstall

```bash
helm uninstall zeta -n zeta-poc
```

The shared PVCs have `helm.sh/resource-policy: keep`, so Helm leaves the cache,
artifacts, and environment workspaces in place. To remove everything:

```bash
kubectl delete namespace zeta-poc
```

For a raw installation:

```bash
make uninstall-platform
```

This deletes the `zeta-poc` namespace: portal, Git server, Jobs, PVCs, and
environment workspaces. Imported images remain in containerd.

## Health check

```bash
curl -fsS http://zeta.local/healthz
```

## Publish the documentation

The MkDocs site is built from `docs/` with Material for MkDocs.

Local preview:

```bash
python3 -m venv .venv-docs
. .venv-docs/bin/activate
pip install -r requirements-docs.txt
mkdocs serve
```

Open <http://127.0.0.1:8000>.

Build only:

```bash
mkdocs build --strict
```

### GitHub Pages

The workflow `.github/workflows/docs.yml` runs on every push to `main` that
changes `docs/`, `mkdocs.yml`, or `requirements-docs.txt`. It installs
`requirements-docs.txt` and runs:

```bash
mkdocs gh-deploy --force
```

This publishes to the `gh-pages` branch. The repository Pages source is:

- branch `gh-pages`;
- folder `/ (root)`.

The site is available at <https://slimaineb.github.io/zeta/>.

If the URL returns a GitHub Pages 404:

1. open **Settings → Pages**;
2. set **Source** to **Deploy from a branch**;
3. choose `gh-pages` and `/ (root)`;
4. save, then wait for the Pages build to finish.

You can also trigger the docs workflow manually from the **Actions** tab.

## Upgrade checklist

1. Review the release notes and `helm show values`.
2. Back up any data you cannot recreate.
3. `helm upgrade --install zeta ... --reuse-values`.
4. Update the CLI with the install script.
5. `zeta login`.
6. Run a smoke build and `zeta ls`.

## Backup notes

- PVC data (caches, artifacts, environment workspaces) is not backed up by the
  portal.
- Back up environment workspaces from inside the pod, or snapshot the underlying
  storage.
- Helm configuration lives in your values file or `--set` overrides. Raw
  configuration lives in `platform/manifests/portal.yaml` and the profiles YAML.
