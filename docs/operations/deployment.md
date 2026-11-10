# Deployment and operations

This page covers day-two operations: deploying updates, inspecting state,
cleaning up, and publishing the documentation.

## Deploy or update the portal

```bash
cd portal
make test
make deploy
```

`make deploy`:

- compiles `portal/bin/zeta`;
- builds the `zeta/portal:0.1` image, including `syncd`;
- imports it into k3s;
- applies `platform/manifests/portal.yaml`;
- restarts the Deployment and waits for rollout.

From the repository root, `make install-platform` rebuilds the images and
reapplies everything.

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

```bash
make install-user
```

Or manually:

```bash
cd cli
make build
sudo install -m 0755 bin/zeta /usr/local/bin/zeta
```

## Status

```bash
make status
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

This publishes to the `gh-pages` branch. In the GitHub repository:

1. open **Settings → Pages**;
2. set **Source** to **Deploy from a branch**;
3. choose the `gh-pages` branch and the `/ (root)` folder;
4. save.

The site is then available at `https://slimaineb.github.io/zeta/`.

You can also trigger the workflow manually from the **Actions** tab.

## Upgrade checklist

1. `git pull`.
2. `make doctor`.
3. `make install-platform` (images and portal).
4. `make install-user` (CLI).
5. `zeta login`.
6. Run a smoke build and `zeta ls`.

## Backup notes

- PVC data (caches, artifacts, environment workspaces) is not backed up by the
  portal.
- Back up environment workspaces from inside the pod, or snapshot the underlying
  storage.
- Configuration lives in `platform/manifests/portal.yaml` and the profiles YAML.
