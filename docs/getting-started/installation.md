# Installation

This page describes a complete installation of the portal, the build images, and
the `zeta` CLI on a single-node k3s cluster. Installation is idempotent and can
be re-run after an update.

## Prerequisites

- Linux or WSL2.
- A single-node k3s cluster reachable with `kubectl --context local`.
- Docker, used only to build and import images.
- `kubectl`, `git`, `make`.
- Go 1.26 or newer to build the portal and CLI.
- Passwordless `sudo -n` for `k3s` and `k3s ctr` image imports.

Check the environment:

```bash
make doctor
```

Or manually:

```bash
kubectl --context local get nodes
docker version
go version
```

!!! warning "kubectl context"
    Always use `--context local` in this repository. The default kubectl context
    may point at another cluster.

## 1. Start k3s under WSL

Without systemd, k3s does not restart automatically. Start it manually:

```bash
sudo -n bash -c 'nohup k3s server >/tmp/k3s-server.log 2>&1 &'

for i in $(seq 1 60); do
  kubectl --context local get nodes >/dev/null 2>&1 && break
  sleep 2
done

kubectl --context local get nodes
```

Wait until `STATUS` is `Ready`.

## 2. Install the platform

The recommended command builds the images, imports them into k3s, applies the
manifests, and waits for the portal rollout:

```bash
make install-platform
```

To deploy the example Git server as well:

```bash
make install-platform WITH_GIT_SERVER=1
```

### What `install-platform` does

`scripts/install-platform.sh`:

1. builds and imports the build images;
2. compiles the portal and builds `zeta/portal:0.1` (which also contains `syncd`);
3. imports the portal image into k3s;
4. applies the namespace, PVCs, and portal manifests;
5. waits for the rollout.

### Build the build images only

```bash
cd platform
make images
```

This builds `zeta/cpp:0.1`, `zeta/python-uv:0.1`, `zeta/go:0.1`, and
`zeta/git-server:0.1`, then imports them into containerd.

Verify:

```bash
sudo -n k3s ctr images ls -q | grep -E 'zeta|busybox'
```

The Go image is based on `golang:1.26-bookworm` and uses:

- `GOCACHE=/cache/go-build`
- `GOMODCACHE=/cache/go-mod`
- `/artifacts/go` as the artifact directory

## 3. Deploy the portal only

After a portal code change:

```bash
cd portal
make deploy
```

This target compiles `portal/bin/zeta`, builds `zeta/portal:0.1`, imports it into
k3s, applies `platform/manifests/portal.yaml`, restarts the Deployment, and waits
for the rollout.

Verify:

```bash
kubectl --context local -n zeta-poc get deploy,svc,ingress,pods
kubectl --context local -n zeta-poc logs deploy/zeta-portal --tail=20
```

## 4. Install the CLI

From the repository root:

```bash
make install-user
```

The binary is installed as `~/.local/bin/zeta`. To choose another directory:

```bash
BIN_DIR=/usr/local/bin make install-user
```

If `~/.local/bin` is not in your `PATH`, the installer prints the line to add to
your shell profile.

Verify:

```bash
zeta help
```

## 5. Access the portal

### Option A — Ingress (recommended)

Get the ingress address:

```bash
kubectl --context local -n zeta-poc get ingress zeta-portal
```

Add a hosts entry on the machine that opens the browser.

On Windows, as Administrator, edit
`C:\Windows\System32\drivers\etc\hosts` and add:

```text
<ADDRESS> zeta.local
```

Example:

```text
172.29.74.141 zeta.local
```

Add the same entry inside WSL so the CLI can use the name:

```bash
echo "<ADDRESS> zeta.local" | sudo tee -a /etc/hosts
```

!!! note "WSL IP"
    The WSL IP may change after a reboot. Update both hosts files if needed.

### Option B — Port-forward

In WSL:

```bash
kubectl --context local -n zeta-poc port-forward \
  --address 0.0.0.0 svc/zeta-portal 8080:80
```

From Windows, open `http://localhost:8080` or `http://<WSL_IP>:8080`.

!!! tip "Windows and port-forward"
    `--address 0.0.0.0` is required for Windows to reach the port-forward.

## 6. Log in

### Development credentials

| Email | Password | Groups |
|---|---|---|
| `dev@example.com` | `dev` | `devs` |
| `admin@example.com` | `admin` | `admins` |
| `alice@example.com` | `alice` | `backend` |

```bash
zeta login \
  --server http://zeta.local \
  --email dev@example.com \
  --password dev
```

In production, `/login` redirects to the configured OIDC provider.

## 7. Verify the installation

```bash
zeta profiles
zeta builds
zeta volumes
kubectl --context local -n zeta-poc get pods,pvc
```

## 8. Optional example Git server

Deploy it with the platform:

```bash
make install-platform WITH_GIT_SERVER=1
```

Or start it separately:

```bash
cd platform
make git-init
```

`git-init` applies `platform/manifests/git-server.yaml`, waits for the
deployment, opens a local port-forward, and pushes the example projects on
`main`.

Verify:

```bash
kubectl --context local -n zeta-poc exec deploy/git-server -- \
  sh -c 'for r in cpp python-uv go; do echo "--- $r"; git --git-dir=/srv/git/$r.git symbolic-ref HEAD; git --git-dir=/srv/git/$r.git show-ref; done'
```

Expected: `refs/heads/main` and one commit per repository.

## 9. Status and uninstall

```bash
make status
```

To remove everything:

```bash
make uninstall-platform
```

This deletes the `zeta-poc` namespace: portal, Git server, Jobs, and PVCs.
Imported images remain in containerd.

!!! danger "Volume data"
    `make uninstall-platform` deletes the PVCs, including caches, artifacts, and
    persistent environment workspaces. Back up anything you need first.

## Production notes

- Replace the `local-path` StorageClass with an EBS-backed class
  (`--storage-class` / `ZETA_STORAGE_CLASS`) for durability and detach
  behavior.
- Configure OIDC and a fixed session key. See
  [Configuration](../reference/configuration.md).
- Enable `--secure-cookie` behind HTTPS.
- Read [Security](../operations/security.md) before exposing the portal.
