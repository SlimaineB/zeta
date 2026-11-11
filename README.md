# Zeta

Zeta is a Kubernetes build portal for developers **without user kubeconfigs**.
Developers submit builds from their workstation with the `zeta` CLI. The portal runs
inside the cluster, creates Kubernetes Jobs with its own ServiceAccount, and
streams logs, metrics, artifacts, and optional bidirectional workspace sync back
to the CLI.

The web UI is a monitoring dashboard, not a build form.

> Full documentation: <https://slimaineb.github.io/zeta/> — sources in [`docs/`](docs/).
>
> - **Platform team:** [install the portal with Helm](docs/platform/installation.md).
> - **Developers:** [install the CLI](docs/developer/installation.md) and run a first build.

## Table of contents

1. [Architecture and benefits](#1-architecture-and-benefits)
2. [Components](#2-components)
3. [Requirements](#3-requirements)
4. [Quick start](#4-quick-start)
5. [Installation in detail](#5-installation-in-detail)
6. [Using the CLI](#6-using-the-cli)
7. [Web UI](#7-web-ui)
8. [Build profiles](#8-build-profiles)
9. [Portal configuration](#9-portal-configuration)
10. [HTTP API](#10-http-api)
11. [Operations](#11-operations)
12. [Troubleshooting](#12-troubleshooting)
13. [Security model and limitations](#13-security-model-and-limitations)
14. [Development](#14-development)
15. [Repository layout](#15-repository-layout)

---

## 1. Architecture and benefits

```text
Developer workstation
  |
  |  zeta build
  v
zeta CLI ---- HTTP + session cookie ----> Zeta portal (in-cluster)
                                         |
                                         | ServiceAccount + namespace RBAC
                                         v
                                   Kubernetes Job
                                     |- init: extract tar context or clone Git
                                     |- build: run profile commands
                                     |- syncd: optional workspace sync sidecar
                                     |- cache PVC
                                     `- artifacts PVC
                                         |
                                         v
                              Portal: logs, status, metrics, artifacts
                                         |
                                         v
                                   Web dashboard
```

Key points:

- Developers never receive a kubeconfig.
- The portal runs in the cluster and uses its own ServiceAccount.
- The default build mode uploads the local working tree as a `tar.gz` context.
- Git mode is optional and builds an already-pushed commit.
- `zeta build --sync` keeps the local workspace and the build pod synchronized.
- Built-in profiles cover C++/CMake, Python/uv, and Go modules.

### Control plane vs. execution plane

Zeta separates the control plane from the execution plane:

| Layer | Runs where | Responsibility |
|---|---|---|
| `zeta` CLI | Developer workstation | Package the context, call the API, stream logs, receive artifacts, run sync |
| Portal | Kubernetes namespace | Authenticate users, validate requests, resolve profiles, create Jobs, relay logs/artifacts/sync |
| Kubernetes | Cluster | Schedule and isolate build pods, mount PVCs, enforce RBAC and Pod Security |
| Build images | Cluster nodes | Provide compilers, package managers, Git, and profile-specific toolchains |

The portal never compiles user code itself. It only creates and observes
Kubernetes Jobs. The actual build runs in an ephemeral pod with the selected
profile image, a workspace volume, a persistent cache volume, and an artifacts
volume.

### Request flow

1. The developer runs `zeta build` from a project directory.
2. The CLI detects the profile, builds a filtered `tar.gz` context, and uploads
   it to the portal with the session cookie.
3. The portal validates the archive, stores it on the context PVC, and creates a
   Job.
4. The Job init container extracts the context into `/workspace` (or clones the
   requested Git commit in Git mode).
5. The build container runs the profile commands with `/cache` and `/artifacts`
   mounted.
6. The portal streams logs and status to the CLI and web UI.
7. When the Job succeeds, the CLI downloads the artifacts as a `.tar` archive.
8. The Job and its pod are removed automatically after the configured TTL.

In sync mode, the flow adds a `syncd` sidecar and a WebSocket relay:

```text
zeta CLI <--- WebSocket ---> portal sync hub <--- WebSocket ---> syncd sidecar
                                                                    |
                                                              /workspace
                                                              /artifacts
```

The build waits for `/workspace/.zeta-ready` before starting. Local changes
are sent to the pod while it runs, and pod-side changes plus artifacts are sent
back before the final flush.

### Design choices

- **Local context first.** The default mode works without a Git remote, a
  webhook, or a pushed commit. This is useful for quick experiments, private
  branches, and monorepos with large histories.
- **Git mode when reproducibility matters.** Passing `--git` or `--repo` builds
  an exact commit that is reachable from the cluster.
- **Profiles instead of hard-coded languages.** A profile is just an image,
  commands, resources, environment, and metrics. Adding a language does not
  require changing the portal API.
- **Persistent caches, ephemeral builds.** Compilers and package caches live on
  PVCs, while Jobs and pods are disposable. PVCs are mounted only while a build
  runs; after the Job is deleted, only the storage remains.
- **No user cluster credentials.** Authentication and authorization happen at
  the portal. Developers interact only with HTTP and the CLI.
- **Observable by default.** Logs, durations, cache metrics, and artifacts are
  exposed through the same API used by the CLI and web dashboard.

### Benefits

- **Compute on demand.** One of the main goals of Zeta is to avoid keeping a
  powerful build machine running all the time. Build compute is requested only
  while a build Job is running, then released. Persistent environments scale to
  zero when idle. A small shared cluster replaces always-on, high-end developer
  workstations for builds.
- **Zero Kubernetes onboarding for developers.** No kubeconfig, no namespace
  access, no `kubectl` knowledge required.
- **Reproducible environments.** Every build uses a pinned image and profile, so
  local machine differences do not affect the result.
- **Isolation and safety.** Builds run as non-root Jobs under `restricted` PSA,
  without service account tokens, and with a read-only portal root filesystem.
- **Fast feedback.** Logs stream live, cache state persists between builds, and
  artifacts are downloaded automatically.
- **Flexible source input.** Use the local working tree by default, or a Git
  commit when you need an auditable, pushed revision.
- **Interactive debugging.** `--sync` lets you edit code locally while the build
  runs and recover files produced inside the pod. `zeta code` opens VS Code in
  the browser on a persistent environment's `/workspace`.
- **Centralized operations.** Jobs, logs, metrics, and artifacts are visible in
  one place and cleaned up by TTL.
- **Extensible by design.** New toolchains are added with a profile and an image,
  not by modifying the control plane.
- **Portable.** The control plane is a normal Kubernetes Deployment; storage uses
  PVCs and can be replaced with another backend later.

What Zeta is not:

- It is not a full CI/CD system with SCM webhooks, pipelines, approvals, and
  deployment stages.
- It does not provide full per-group RBAC or multi-tenant isolation yet;
  builds and environments are owner-scoped with an admin override.
- It does not build or push OCI images; it produces build artifacts on a PVC.
- It does not replace a remote cache or distributed build system for very large
  monorepos.

---

## 2. Components

| Component | Role |
|---|---|
| `portal` | REST API, dev/OIDC auth, embedded web UI, Kubernetes Job creation |
| `cli zeta` | Login, build submission, logs, status, metrics, artifacts |
| Git server | Optional example server with `cpp.git`, `python-uv.git`, `go.git` |
| Build Job | Extracts context or clones Git, runs profile commands, writes `/artifacts` |
| Cache PVC | Persistent `ccache`, `uv`, and Go module/build caches |
| Context PVC | Uploaded `tar.gz` contexts for local-context builds |
| Artifacts PVC | Build outputs shared between Jobs and the portal |

Kubernetes resources:

- Namespace: `zeta-poc`
- Pod Security Admission: `restricted`
- Build Jobs: non-root `1000:1000`, no service account token, seccomp
  `RuntimeDefault`, all capabilities dropped
- Portal: non-root `65532:65532`, read-only root filesystem
- Portal RBAC: namespace-scoped access to Jobs, Pods, Pod logs, and PVCs

---

## 3. Requirements

Published installation:

- Kubernetes 1.27+ with an Ingress controller.
- Helm 3.14+.
- `kubectl`.
- A StorageClass: `local-path` on a single node, or an EBS/gp3 class in
  production.

Source installation additionally needs:

- Linux or WSL2.
- A single-node k3s cluster reachable with `kubectl --context local`.
- Docker, used only to build and import images.
- `kubectl`, `git`, `make`.
- Go 1.26 or newer for building the portal and CLI.
- Passwordless `sudo -n` for `k3s` and `k3s ctr` image imports.

Check a source environment:

```bash
make doctor
```

Or manually:

```bash
kubectl --context local get nodes
docker version
go version
```

Always use `--context local` for the source path. The default kubectl context may
point at another cluster.

---

## 4. Quick start

### Platform team

```bash
helm upgrade --install zeta oci://ghcr.io/slimaineb/charts/zeta \
  --version 0.1.0 \
  --namespace zeta-poc --create-namespace \
  --set ingress.host=zeta.local
```

The published chart points at the GHCR images. See
[platform installation](docs/platform/installation.md) for OIDC, storage,
private registries, and upgrades.

### Developers

Linux and macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/SlimaineB/zeta/main/scripts/install-cli.sh | sh
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/SlimaineB/zeta/main/scripts/install-cli.ps1 | iex
```

Then:

```bash
zeta login --server http://zeta.local --email dev@example.com --password dev
cd my-project
zeta build
```

See [developer installation](docs/developer/installation.md) and
[first build](docs/getting-started/quickstart.md).

### From source

On a local k3s cluster, build the images and install the local Helm chart:

```bash
./scripts/install.sh
```

`make install-smart` is the same command. The older raw path is still available:

```bash
make install-platform
make install-platform WITH_GIT_SERVER=1
make install-user
```

`make install` now runs `install-smart` and `install-user`.

---

## 5. Installation in detail

The quick start above is the recommended path for both audiences. The rest of
this section describes the local source installation on k3s; the
[platform documentation](docs/platform/installation.md) covers Helm, OIDC,
storage, private registries, upgrades, and uninstall.

### 5.1 Start k3s under WSL

Without systemd, k3s does not restart automatically:

```bash
sudo -n bash -c 'nohup k3s server >/tmp/k3s-server.log 2>&1 &'

for i in $(seq 1 60); do
  kubectl --context local get nodes >/dev/null 2>&1 && break
  sleep 2
done

kubectl --context local get nodes
```

Wait until `STATUS` is `Ready`.

### 5.2 Build and import images

```bash
cd platform
make images
```

This target:

- builds `zeta/cpp:0.1`, `zeta/python-uv:0.1`,
  `zeta/go:0.1`, and `zeta/git-server:0.1`;
- pulls `busybox:1.36` and `alpine:3.20`;
- imports all images into k3s containerd.

Verify:

```bash
sudo -n k3s ctr images ls -q | grep -E 'zeta|busybox'
```

The Go image is based on `golang:1.26-bookworm`. It uses:

- `GOCACHE=/cache/go-build`
- `GOMODCACHE=/cache/go-mod`
- artifact directory `/artifacts/go`

### 5.3 Deploy the portal

The recommended path is:

```bash
make install-platform
```

This runs `scripts/install-platform.sh`, which builds the images, imports the
portal image, applies the manifests, and waits for the rollout.

To deploy only the portal after a code change:

```bash
cd portal
make deploy
```

This target:

- compiles `portal/bin/zeta`;
- builds the `zeta/portal:0.1` image, including `syncd`;
- imports it into k3s;
- applies `platform/manifests/portal.yaml`;
- restarts the Deployment and waits for rollout.

Verify:

```bash
kubectl --context local -n zeta-poc get deploy,svc,ingress,pods
kubectl --context local -n zeta-poc logs deploy/zeta-portal --tail=20
```

### 5.4 Optional example Git server

Deploy it with the platform:

```bash
make install-platform WITH_GIT_SERVER=1
```

Or start it separately:

```bash
cd platform
make git-init
```

`git-init` applies `platform/manifests/git-server.yaml`, waits for the deployment,
opens a local port-forward, and pushes the example projects on `main`.

Verify:

```bash
kubectl --context local -n zeta-poc exec deploy/git-server -- \
  sh -c 'for r in cpp python-uv go; do echo "--- $r"; git --git-dir=/srv/git/$r.git symbolic-ref HEAD; git --git-dir=/srv/git/$r.git show-ref; done'
```

Expected: `refs/heads/main` and one commit per repository.

### 5.5 Install the CLI

Users should install the release binary:

```bash
curl -fsSL https://raw.githubusercontent.com/SlimaineB/zeta/main/scripts/install-cli.sh | sh
```

Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/SlimaineB/zeta/main/scripts/install-cli.ps1 | iex
```

For a source build from the repository root:

```bash
make install-user
```

The binary is installed as `~/.local/bin/zeta`. To choose another directory:

```bash
BIN_DIR=/usr/local/bin make install-user
```

If `~/.local/bin` is not in `PATH`, the installer prints the line to add to your
shell profile. Verify:

```bash
zeta help
```

### 5.6 Access the portal

#### Option A: Ingress (recommended)

Get the ingress address:

```bash
kubectl --context local -n zeta-poc get ingress zeta-portal
```

Add a hosts entry on the machine that opens the browser.

Windows, as Administrator, edit:

```text
C:\Windows\System32\drivers\etc\hosts
```

Add:

```text
<ADDRESS> zeta.local
```

Example:

```text
172.29.74.141 zeta.local
```

Also add the same entry inside WSL so the CLI can use the name:

```bash
echo "<ADDRESS> zeta.local" | sudo tee -a /etc/hosts
```

If the WSL IP changes after a reboot, update both hosts files.

#### Option B: Port-forward

In WSL:

```bash
kubectl --context local -n zeta-poc port-forward \
  --address 0.0.0.0 svc/zeta-portal 8080:80
```

From Windows:

```text
http://localhost:8080
```

or:

```text
http://<WSL_IP>:8080
```

`--address 0.0.0.0` is required for Windows to reach the port-forward.

#### Development credentials

| Email | Password | Groups |
|---|---|---|
| `dev@example.com` | `dev` | `devs` |
| `admin@example.com` | `admin` | `admins` |
| `alice@example.com` | `alice` | `backend` |

In production, `/login` redirects to the configured OIDC provider.

### 5.7 Status and uninstall

```bash
make status
```

Uninstall:

```bash
make uninstall-platform
```

This deletes the `zeta-poc` namespace: portal, Git server, Jobs, and PVCs.
Imported images remain in containerd.

---

## 6. Using the CLI

### 6.1 Login

Development mode:

```bash
zeta login \
  --server http://zeta.local \
  --email dev@example.com \
  --password dev
```

The session is stored in `~/.config/zeta/config.json` with mode `0600`. The server
can also be provided with `ZETA_SERVER`.

OIDC browser login is not integrated into the CLI yet. For now, copy the
`zeta_session` cookie from the browser and run:

```bash
zeta login --server https://zeta.example.com --cookie '<cookie_value>'
```

### 6.2 Profiles

```bash
zeta profiles
```

Example:

```text
NOM     LANGAGE  DÉPÔT PAR DÉFAUT                DESCRIPTION
cpp     cpp      git://git-server/cpp.git        C++ CMake/Ninja avec cache ccache persistant
go      go       git://git-server/go.git         Go modules avec cache GOCACHE/GOMODCACHE persistant
python  python   git://git-server/python-uv.git  Python 3.12 + uv avec cache uv persistant
```

### 6.3 Build from a local context

From a project directory:

```bash
cd my-project
zeta build
```

The CLI:

- auto-detects the profile:
  - `CMakeLists.txt` -> `cpp`
  - `go.mod` -> `go`
  - `pyproject.toml`, `setup.py`, or `requirements.txt` -> `python`
- creates a `tar.gz` of the local context and uploads it;
- excludes `.git`, `.venv`, `build`, `dist`, `__pycache__`, `node_modules`,
  caches, `.zeta*`, and the artifacts directory by default;
- streams logs live;
- waits for completion;
- downloads and extracts artifacts into `artifacts/<id>`;
- exits `0` on success and `1` on failure.

Example output:

```text
contexte: /home/user/my-project (27 KB)
build zeta-cpp-xxxxxxxxxxxx (cpp) contexte /home/user/my-project
--- logs ---
[ live build logs ]
build réussi en 5.0 s
cache: 7 hits / 1 miss (88%) · 43 KB
artefacts: /home/user/my-project/artifacts/zeta-cpp-xxxxxxxxxxxx
```

Useful options:

```bash
# Force a profile
zeta build --profile go

# Add extra exclusions
zeta build --exclude .cache --exclude docs

# Bidirectional sync during the build
zeta build --sync

# Do not follow logs
zeta build --follow=false

# Do not download artifacts
zeta build --artifacts=false

# Change the artifact output directory
zeta build --artifacts-dir ./out

# Change the timeout (default 30m)
zeta build --timeout 10m

# Build a different local directory
zeta build --context /path/to/project
```

### 6.4 Interactive sync

`zeta build --sync` opens a WebSocket tunnel between the CLI and the `syncd`
sidecar in the build pod:

- the initial context is synchronized before the build starts;
- the build waits for the `/workspace/.zeta-ready` marker;
- local edits are sent to the pod while the build runs;
- files created in `/workspace` and build artifacts are sent back to the CLI;
- a final flush writes the latest artifacts to `artifacts/<id>` before exit.

Default exclusions prevent local build outputs such as `build`, `dist`, and
`.venv` from being overwritten. `--sync` is incompatible with Git mode. The sync
build uses the same timeout as the build (default 30 minutes).

### 6.5 Git mode

Git mode builds a commit that is already pushed and reachable from the cluster:

```bash
zeta build --git --profile cpp \
  --repo git://git-server/cpp.git \
  --commit main
```

With the example Git server, the in-cluster URL is
`git://git-server/<repo>.git`. To clone locally, use a port-forward:

```bash
kubectl --context local -n zeta-poc port-forward svc/git-server 19418:9418
git clone git://127.0.0.1:19418/cpp.git
```

The local `127.0.0.1:19418` remote is not reachable from the cluster. For builds,
use the internal URL:

```bash
zeta build --git --profile cpp --repo git://git-server/cpp.git --commit main
```

`--commit` requires `--git` or `--repo`. A local uncommitted or unpushed commit
cannot be cloned by the Job.

### 6.6 Inspect existing builds

List builds:

```bash
zeta builds
```

Show details:

```bash
zeta status zeta-cpp-xxxxxxxxxxxx
```

Stream logs from an existing build:

```bash
zeta logs zeta-cpp-xxxxxxxxxxxx
```

Show cache metrics:

```bash
zeta metrics zeta-cpp-xxxxxxxxxxxx
```

Example:

```json
{
  "hits": 5,
  "misses": 1,
  "hitRate": 83.33,
  "sizeBytes": 43628
}
```

Download artifacts:

```bash
zeta artifacts -o artifacts.tar zeta-cpp-xxxxxxxxxxxx
```

Flags must come before the build ID. The archive contains the contents of the
profile's `artifactPath`. `zeta build` already downloads and extracts artifacts by
default.

### 6.7 Volumes and last use

List the PVCs managed by Zeta:

```bash
zeta volumes
```

Example:

```text
NOM                      ROLE       TAILLE  DERNIÈRE UTILISATION            BUILD
zeta-poc-artifacts  artifacts  1.0 GB  2026-11-08 18:59 (à l'instant)  zeta-go-8415dfd8487a
zeta-poc-cache      cache      2.0 GB  2026-11-08 18:59 (à l'instant)  zeta-go-8415dfd8487a
zeta-poc-contexts   context    2.0 GB  2026-11-08 18:59 (à l'instant)  zeta-go-8415dfd8487a
```

Filter for recycling:

```bash
zeta volumes --role cache
zeta volumes --older-than 720h
zeta volumes --json
```

`--older-than` keeps volumes whose last build is older than the given duration.
It is a reporting filter: Zeta does not delete volumes automatically.

Expand or delete a managed volume:

```bash
zeta volumes resize zeta-poc-cache 5Gi
zeta volumes delete zeta-test-cache
```

The same actions are available from the **Volumes** panel in the web UI. A
volume cannot be resized or deleted while a build is using it. Expansion also
requires a StorageClass with `allowVolumeExpansion: true`.

The quota is displayed above the volume table:

```text
quota: 5.0 GiB / 20 GiB used, 15 GiB free (max per volume: 10 GiB)
```

### 6.8 Logout

```bash
zeta logout
```

This only removes the local session.

### 6.9 Complete example

```bash
zeta login --server http://zeta.local --email dev@example.com --password dev
cd my-project
zeta build
# live logs, then:
# build réussi en 5.0 s
# cache: 7 hits / 1 miss (88%) · 43 KB
# artefacts: /home/user/my-project/artifacts/zeta-cpp-xxxxxxxxxxxx

find artifacts -maxdepth 3 -type f
zeta builds
zeta status zeta-cpp-xxxxxxxxxxxx
zeta metrics zeta-cpp-xxxxxxxxxxxx
zeta artifacts -o /tmp/build.tar zeta-cpp-xxxxxxxxxxxx
```

### 6.10 Persistent environments

An environment is a long-lived pod backed by a PVC and kept in sync with your
local directory. Unlike a build, it stays available between commands.

```bash
zeta init --name dev --profile go --context .   # PVC + secret + stopped deployment
zeta activate --name dev --context .            # start it, open a shell in /workspace, sync files
zeta ls                                         # list environments
zeta deactivate --name dev                      # stop it (scale to zero), keep the volume
zeta rm dev                                     # delete everything, including the volume
```

Most options can live in a `zeta.yaml` file at the project root, so the commands
stay short:

```yaml
name: dev
profile: go
image: zeta/go:0.1        # optional base image override
context: .                # directory mirrored into /workspace
size: 5Gi
exclude:
  - artifacts
env:
  GOFLAGS: -mod=mod
  APP_ENV: development
```

CLI flags override `zeta.yaml`. `zeta.yaml` is read from the `--context`
directory (default `.`), and `context` is resolved relative to it.

Inside the shell, run any command (`go build`, `make`, ...). The workspace is
mirrored bidirectionally: local edits appear in the pod and build outputs
(`build/`, `dist/`) come back to your machine. `.git`, `node_modules`, caches and
other generated directories are excluded.

To save compute, the portal automatically scales an environment back to zero
when no shell or sync client has been attached for `--env-idle-timeout`
(default `15m`, `0` disables it). The volume and its contents are preserved, so
the next `zeta activate` resumes quickly.

---

## 7. Web UI

The UI is a monitoring dashboard.

- Summary cards: total builds, running builds, success rate, average duration,
  average cache hit rate.
- Charts: recent build durations and cache hit evolution.
- Build table: profile, repository, commit, status, duration, cache.
- Cache metrics: hit rate, hits/misses, size.
- **Logs** button: live logs in a modal.
- **Artifacts** button: download the `.tar` for a successful build.
- Light/dark theme, remembered in the browser.
- Auto-refresh every 4 seconds.
- **Refresh** and **Logout** buttons.

Builds are launched from the CLI, not from the UI.

---

## 8. Build profiles

Profiles are loaded from `portal/internal/builds/defaults.yaml` and merged with
the optional `--profiles-file` / `ZETA_PROFILES_FILE` file. Merging is by
name: a custom profile can replace `cpp`, `python`, or `go`, or add a new one.

### 8.1 Schema

```yaml
profiles:
  - name: cpp
    description: C++ CMake/Ninja with persistent ccache
    language: cpp
    image: zeta/cpp:0.1
    defaultRepo: git://git-server/cpp.git
    workDir: /workspace
    artifactPath: /artifacts/cpp
    cacheDir: /cache/ccache
    # Optional: use a dedicated cache PVC instead of the shared one
    cachePVC: zeta-cpp-cache
    cacheSize: 5Gi
    # Optional: use a dedicated artifacts PVC instead of the shared one
    artifactsPVC: zeta-cpp-artifacts
    artifactsSize: 2Gi
    # Optional: StorageClass for dedicated volumes (default: portal setting)
    storageClass: ebs-sc
    # Optional: files never copied back from the build pod
    artifactExclude:
      - "*.o"
      - "*.d"
    # Optional: concurrent builds for this profile (default: 1)
    maxConcurrent: 1
    envFromSecrets:
      - zeta-build-secrets
    resources:
      cpuRequest: 200m
      memoryRequest: 256Mi
      cpuLimit: "2"
      memoryLimit: 2Gi
      ephemeralStorage: 2Gi
    commands:
      - |
        export CCACHE_DIR=/cache/ccache
        export CCACHE_MAXSIZE=2G
        export CCACHE_BASEDIR=/workspace
        cmake -S . -B build -G Ninja -DCMAKE_BUILD_TYPE=Release \
          -DCMAKE_C_COMPILER_LAUNCHER=ccache \
          -DCMAKE_CXX_COMPILER_LAUNCHER=ccache
        cmake --build build
        cmake --install build --prefix /artifacts/cpp
    metrics:
      - name: ccache_hits
        command: ccache -s | awk '/Hits:/ {print $2}'
      - name: ccache_misses
        command: ccache -s | awk '/Misses:/ {print $2}'
      - name: cache_bytes
        command: du -sb /cache/ccache | cut -f1
```

| Field | Required | Description |
|---|---|---|
| `name` | yes | Profile identifier |
| `language` | yes | Displayed language |
| `image` | yes | Build image |
| `commands` | yes | Shell commands executed in `workDir` |
| `artifactPath` | yes | Directory exported as a `.tar` |
| `description` | no | Display text |
| `defaultRepo` | no | Default repository |
| `workDir` | no | Defaults to `/workspace` |
| `cacheDir` | no | Informational cache path |
| `cachePVC` | no | Dedicated cache PVC name; defaults to the shared cache PVC |
| `cacheSize` | no | Size used when creating `cachePVC`; defaults to `2Gi` |
| `artifactsPVC` | no | Dedicated artifacts PVC name; defaults to the shared artifacts PVC |
| `artifactsSize` | no | Size used when creating `artifactsPVC`; defaults to `1Gi` |
| `storageClass` | no | StorageClass for dedicated PVCs; defaults to the portal `--storage-class` |
| `artifactExclude` | no | Glob patterns excluded from artifact download and sync |
| `maxConcurrent` | no | Maximum concurrent builds for this profile; defaults to `1` |
| `env` | no | Literal environment variables injected into `context` and `build` |
| `envFromSecrets` | no | Optional Secrets injected with `envFrom` |
| `resources` | no | CPU, memory, and ephemeral-storage requests/limits |
| `metrics` | no | Metric commands run after the build |

### 8.2 Built-in profiles

| Profile | Language | Image | Artifacts | Cache |
|---|---|---|---|---|
| `cpp` | C++ | `zeta/cpp:0.1` | `/artifacts/cpp` | `/cache/ccache` |
| `python` | Python 3.12 + uv | `zeta/python-uv:0.1` | `/artifacts/python` | `/cache/uv` |
| `go` | Go | `zeta/go:0.1` | `/artifacts/go` | `/cache/go-build`, `/cache/go-mod` |

The Go profile:

- sets `PATH` so `/usr/local/go/bin` is available in the build container;
- sets `GOCACHE` and `GOMODCACHE`;
- runs `go mod download`, `go test ./...`, and either `go build ./cmd/...` or
  `go build .` depending on the project layout;
- reports `cache_bytes`.

### 8.3 Environment variables and Secrets

`env` injects literal variables. `envFromSecrets` references Kubernetes Secrets.
Secret references are marked `optional: true`, so a build still runs when the
Secret does not exist.

To provide repository or private-module credentials, create the
`zeta-build-secrets` Secret in the namespace:

```bash
kubectl --context local -n zeta-poc create secret generic zeta-build-secrets \
  --from-literal=GIT_TOKEN=ghp_xxx \
  --from-literal=GIT_USERNAME=x-access-token \
  --from-literal=GOPRIVATE=github.com/my-org/* \
  --from-literal=GOPROXY=https://proxy.golang.org,direct
```

The `cpp`, `python`, and `go` profiles already reference it. If `GIT_TOKEN` is
present, the portal configures a Git HTTPS credential helper in the init
container and in the build container when `git` is available.

Supported variables include:

- `GIT_TOKEN`
- `GIT_USERNAME`
- `GOPRIVATE`
- `GOPROXY`
- `GONOSUMDB`
- `GONOPROXY`

Private SSH repositories are not supported.

### 8.4 Metrics

After the build commands, the portal runs `metrics` commands with `set +e`.
Each line in this format is parsed:

```text
ZETA_METRIC name=value
```

Known metrics:

- `ccache_hits`
- `ccache_misses`
- `cache_bytes`

Other metrics are kept in `extra` and displayed as chips.

### 8.5 Custom profiles in Kubernetes

Create a ConfigMap:

```bash
kubectl --context local -n zeta-poc create configmap zeta-profiles \
  --from-file=profiles.yaml=./profiles.yaml
```

Add a volume and the `-profiles-file=/etc/zeta/profiles.yaml` argument to
the Deployment:

```bash
kubectl --context local -n zeta-poc patch deploy zeta-portal --type=json -p='[
  {"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"-profiles-file=/etc/zeta/profiles.yaml"},
  {"op":"add","path":"/spec/template/spec/containers/0/volumeMounts","value":[{"name":"profiles","mountPath":"/etc/zeta","readOnly":true}]},
  {"op":"add","path":"/spec/template/spec/volumes","value":[{"name":"profiles","configMap":{"name":"zeta-profiles"}}]}
]'
kubectl --context local -n zeta-poc rollout status deploy/zeta-portal
```

If the Deployment already has volumes, adapt the patch instead of replacing the
arrays.

### 8.6 Volume strategy and recycling

Zeta separates three roles:

| Role | Default PVC | Mount | Contents |
|---|---|---|---|
| `cache` | `zeta-poc-cache` | `/cache` | Compiler, package, and module caches |
| `artifacts` | `zeta-poc-artifacts` | `/artifacts` | Build outputs |
| `context` | `zeta-poc-contexts` | `/contexts` | Uploaded local contexts |

By default all profiles share these PVCs. Set `cachePVC` / `artifactsPVC` in a
profile to give it dedicated volumes instead. A dedicated volume is created on
first use with the requested size and `storageClass`, then reused on later
builds. Existing PVCs are never resized or recreated automatically; change the
PVC explicitly if you need a larger volume.

PVCs are mounted only for the lifetime of a build pod. When the Job finishes,
the pod and its mounts disappear; only the PVC storage remains. This is the same
cost model as an EBS volume attached during a build and detached afterwards.

The portal labels managed PVCs with `zeta.io/managed-by=portal` and
`zeta.io/volume-role`, and records the last use in annotations:

- `zeta.io/last-used-at`
- `zeta.io/last-build`
- `zeta.io/last-profile`
- `zeta.io/last-owner`

Use `zeta volumes` to inspect capacity and last use before deleting a volume:

```bash
zeta volumes --older-than 720h
kubectl --context local -n zeta-poc delete pvc <name>
```

On k3s the default StorageClass is `local-path`, so volumes are node-local. In
production, set the portal `--storage-class` / `ZETA_STORAGE_CLASS` to an
EBS-backed StorageClass, or set `storageClass` per profile for dedicated
volumes. This gives the expected durability and attach/detach behavior.

The portal enforces two quotas:

- `--volume-quota` / `ZETA_VOLUME_QUOTA` (default `20Gi`): total capacity of
  all managed volumes.
- `--volume-max` / `ZETA_VOLUME_MAX` (default `10Gi`): maximum size of one
  volume.

Quotas are checked when a volume is created and when it is expanded. They are
application-level quotas, not Kubernetes `ResourceQuota` objects, so the portal
can return a clear error in the UI.

### 8.7 Concurrent builds and artifact exclusions

Each profile defaults to `maxConcurrent: 1`. This serializes builds of the same
profile because they write to the same `artifactPath` on the shared artifacts
PVC. Builds of different profiles can still run in parallel.

The portal checks active Jobs before creating a new one and holds an in-process
lock around the check and Job creation. The Deployment runs one replica; with
multiple replicas, use a stronger distributed lock or one profile per replica.

Set `maxConcurrent` above `1` only when the profile writes to a unique
`artifactPath` per build, otherwise concurrent builds can overwrite each other's
artifacts.

To keep large or sensitive files out of the local download, configure
`artifactExclude` in the profile:

```yaml
artifactExclude:
  - "*.log"
  - "coverage/"
  - "**/*.tmp"
```

The portal passes these patterns to `tar --exclude` before streaming artifacts.
The CLI also applies them while extracting locally, and the sync client ignores
them when receiving artifacts. Use `zeta build --artifact-exclude PATTERN` for a
one-off exclusion; the flag can be repeated.

---

## 9. Portal configuration

### 9.1 Flags and environment variables

| Flag | Environment variable | Default | Description |
|---|---|---|---|
| `--addr` | `ZETA_ADDR` | `:8080` | Listen address |
| `--namespace` | `ZETA_NAMESPACE` | `zeta-poc` | Build namespace |
| `--kubeconfig` | `ZETA_KUBECONFIG` | empty | Local kubeconfig (development) |
| `--master` | `ZETA_MASTER` | empty | Kubernetes API URL |
| `--profiles-file` | `ZETA_PROFILES_FILE` | empty | Additional profiles YAML |
| `--image-registry` | `ZETA_IMAGE_REGISTRY` | empty | Registry prefix for profile and helper images |
| `--image-tag` | `ZETA_IMAGE_TAG` | empty | Tag override for profile and helper images |
| `--storage-class` | `ZETA_STORAGE_CLASS` | `local-path` | StorageClass for shared PVCs |
| `--volume-quota` | `ZETA_VOLUME_QUOTA` | `20Gi` | Total capacity of managed volumes |
| `--volume-max` | `ZETA_VOLUME_MAX` | `10Gi` | Maximum size of one managed volume |
| `--env-idle-timeout` | `ZETA_ENV_IDLE_TIMEOUT` | `15m` | Scale idle environments to zero (`0` disables) |
| `--oidc-issuer` | `ZETA_OIDC_ISSUER` | empty | OIDC issuer |
| `--oidc-discovery-url` | `ZETA_OIDC_DISCOVERY_URL` | issuer | OIDC discovery URL |
| `--oidc-authorization-url` | `ZETA_OIDC_AUTHORIZATION_URL` | discovery | OIDC authorization endpoint override |
| `--oidc-client-id` | `ZETA_OIDC_CLIENT_ID` | `zeta-portal` | OIDC client ID |
| `--oidc-client-secret` | `ZETA_OIDC_CLIENT_SECRET` | empty | OIDC client secret |
| `--oidc-redirect-url` | `ZETA_OIDC_REDIRECT_URL` | auto | OIDC callback URL |
| `--oidc-scopes` | `ZETA_OIDC_SCOPES` | `openid,profile,email,groups` | OIDC scopes |
| `--oidc-allowed-groups` | `ZETA_OIDC_ALLOWED_GROUPS` | empty | Comma-separated allowed groups |
| `--oidc-allowed-users` | `ZETA_OIDC_ALLOWED_USERS` | empty | Comma-separated allowed usernames, emails, or subjects |
| `--oidc-username-claim` | `ZETA_OIDC_USERNAME_CLAIM` | `preferred_username` | Username claim |
| `--oidc-email-claim` | `ZETA_OIDC_EMAIL_CLAIM` | `email` | Email claim |
| `--oidc-groups-claim` | `ZETA_OIDC_GROUPS_CLAIM` | `groups` | Groups claim |
| `--oidc-session-key` | `ZETA_OIDC_SESSION_KEY` | random | Session HMAC key |
| `--oidc-session-ttl` | `ZETA_OIDC_SESSION_TTL` | `12h` | Session lifetime |
| `--oidc-dev` | `ZETA_OIDC_DEV` | `false` | Local development auth |
| `--secure-cookie` | `ZETA_SECURE_COOKIE` | `false` | `Secure` cookies |

Rules:

- Either `--oidc-issuer` or `--oidc-dev` is required.
- `--namespace` is required.
- If `--oidc-session-key` is empty, a random key is generated at startup. All
  sessions are invalidated on restart. Set a fixed key in production.

### 9.2 OIDC in production

Example Deployment arguments:

```yaml
args:
  - -addr=:8080
  - -namespace=zeta-poc
  - -oidc-issuer=https://idp.example.com/realms/dev
  - -oidc-discovery-url=https://idp.example.com/realms/dev
  - -oidc-client-id=zeta-portal
  - -oidc-client-secret=<secret>
  - -oidc-redirect-url=https://zeta.example.com/oidc/callback
  - -oidc-allowed-groups=zeta-users
  - -oidc-username-claim=preferred_username
  - -oidc-email-claim=email
  - -oidc-groups-claim=groups
  - -oidc-session-key=<32+ random characters>
  - -oidc-session-ttl=12h
  - -secure-cookie
```

The OIDC provider must allow exactly the callback URL. The portal uses the
configured `--oidc-username-claim`, `--oidc-email-claim`, and
`--oidc-groups-claim`; defaults are `preferred_username`, `email`, and `groups`.
Groups and profile data are also read from the userinfo endpoint when the ID
token does not contain them.

`--oidc-allowed-groups` and `--oidc-allowed-users` are optional allowlists. When
set, the user must match at least one allowed group and at least one allowed
user entry. A user entry can be a username, email, or subject.

Use `--oidc-discovery-url` when the portal must reach discovery through an
internal address while the browser uses a public issuer. Use
`--oidc-authorization-url` when the discovery document advertises an internal
authorization endpoint that the browser cannot reach.

The login flow uses PKCE, verifies the ID token nonce, and stores its short-lived
state in a signed `zeta_oidc` cookie, so it works across multiple portal
replicas. Sessions are stored in the signed `zeta_session` cookie. Environments
and builds are owner-scoped: lifecycle, exec, sync, logs, metrics, and artifact
downloads require the owner or an `admin` / `admins` group member. Listing
builds and environments remains available to every authenticated user. Full
per-group RBAC is still not implemented.

### 9.3 Kubernetes resources

The Helm chart in `charts/zeta` is the recommended installation path. The raw
manifests remain for local development.

- `charts/zeta/`: portal, RBAC, PVCs, Ingress, and optional Git server
- `platform/manifests/namespace.yaml`: namespace and Pod Security labels
- `platform/manifests/pvc.yaml`: shared artifacts PVC
- `platform/manifests/cache-pvc.yaml`: shared build cache PVC
- `platform/manifests/context-pvc.yaml`: shared local context PVC
- `platform/manifests/portal.yaml`: ServiceAccount, RBAC, Deployment, Service, Ingress
- `platform/manifests/git-server.yaml`: optional example Git server

---

## 10. HTTP API

All `/api/*` routes require a session cookie named `zeta_session`, except
the sync agent endpoint which uses a per-build or per-environment bearer token.

| Method | Route | Role |
|---|---|---|
| `POST` | `/api/login` | Development login |
| `GET` | `/oidc/login` | Start OIDC flow |
| `GET` | `/oidc/callback` | OIDC callback |
| `POST` | `/api/logout` | Close session |
| `GET` | `/api/me` | Current user |
| `GET` | `/api/profiles` | Available profiles |
| `GET` | `/api/builds` | List builds |
| `POST` | `/api/builds` | Submit a Git build (JSON) or local-context build (multipart) |
| `GET` | `/api/sync/{id}/client` | CLI-side sync WebSocket |
| `GET` | `/api/sync/{id}/agent` | Sidecar sync WebSocket (bearer token) |
| `GET` | `/api/builds/{id}/logs` | Streaming logs |
| `GET` | `/api/builds/{id}/metrics` | Cache metrics |
| `GET` | `/api/builds/{id}/artifact` | Artifacts as `.tar` |
| `GET` | `/api/volumes` | Managed PVCs, quota, capacity and last use |
| `POST` | `/api/volumes/{name}/resize` | Expand a managed volume (`{"size":"5Gi"}`) |
| `DELETE` | `/api/volumes/{name}` | Delete an idle managed volume |
| `GET` | `/healthz` | Health check |

Build logs, metrics, and artifacts require the build owner or an `admin` /
`admins` group member. Environment lifecycle, exec, and sync endpoints require
the environment owner or an admin. Listing builds and environments remains
available to all authenticated users.

### 10.1 curl examples

```bash
BASE=http://zeta.local

curl -c cookies.txt \
  -H 'Content-Type: application/json' \
  -d '{"username":"dev@example.com","password":"dev"}' \
  "$BASE/api/login"

curl -b cookies.txt "$BASE/api/me"
curl -b cookies.txt "$BASE/api/profiles"

curl -b cookies.txt \
  -H 'Content-Type: application/json' \
  -d '{"profile":"cpp","repo":"git://git-server/cpp.git","commit":"main"}' \
  "$BASE/api/builds"

curl -b cookies.txt "$BASE/api/builds"
curl -b cookies.txt "$BASE/api/volumes"
curl -b cookies.txt "$BASE/api/builds/<id>/logs"
curl -b cookies.txt "$BASE/api/builds/<id>/metrics"
curl -b cookies.txt -o artifacts.tar "$BASE/api/builds/<id>/artifact"

# Local context build
curl -b cookies.txt \
  -F profile=go \
  -F sync=true \
  -F context=@context.tar.gz \
  "$BASE/api/builds"
```

If `zeta.local` is not in `/etc/hosts`, use the ingress IP with a Host
header:

```bash
curl -H 'Host: zeta.local' http://172.29.74.141/api/me
```

### 10.2 Submission formats

Git mode (`application/json`):

```json
{
  "profile": "go",
  "repo": "git://git-server/go.git",
  "commit": "main"
}
```

Local context mode (`multipart/form-data`): fields `profile`, `repo`, `commit`,
`sync`, and file `context` (`tar.gz`). `sync=true` enables the bidirectional sync
tunnel.

Constraints:

- `profile` must exist.
- In Git mode, `repo` must be a `git://`, `http://`, `https://`, or `ssh://` URL.
- In Git mode, `commit` may be empty, `HEAD`, a branch, a tag, or a SHA.
- In context mode, the upload is limited to 512 MiB (2 GiB uncompressed).
- Absolute paths, `..`, out-of-context links, and special files are rejected in
  context archives.
- Shell metacharacters and `..` are rejected in Git references.

---

## 11. Operations

For Helm installs, upgrade with:

```bash
helm upgrade --install zeta oci://ghcr.io/slimaineb/charts/zeta \
  --version 0.2.0 --namespace zeta-poc --reuse-values
```

See [deployment and operations](docs/operations/deployment.md).

### 11.1 Portal logs

```bash
kubectl --context local -n zeta-poc logs deploy/zeta-portal -f
```

or:

```bash
cd portal
make logs
```

### 11.2 Build state

```bash
kubectl --context local -n zeta-poc get jobs,pods -o wide
kubectl --context local -n zeta-poc describe pod <pod>
```

### 11.3 Rebuild and redeploy the portal

After changing Go code or UI assets:

```bash
cd portal
make test
make deploy
```

`make deploy` rebuilds and imports the image, then restarts the Deployment.

### 11.4 Rebuild the CLI

```bash
cd cli
make test
make build
sudo install -m 0755 bin/zeta /usr/local/bin/zeta
```

### 11.5 Restart k3s under WSL

```bash
sudo -n bash -c 'nohup k3s server >/tmp/k3s-server.log 2>&1 &'
kubectl --context local get nodes
```

### 11.6 Clean up builds

Jobs have a 1-hour TTL (`TTLSecondsAfterFinished: 3600`). They disappear from the
cluster and the UI automatically. Every Job also has `ActiveDeadlineSeconds`:
`3600` for normal builds and `1800` for interactive sync builds, so a stuck
build cannot run forever.

Manual deletion:

```bash
kubectl --context local -n zeta-poc delete job \
  -l zeta.io/managed-by=portal
```

### 11.7 Reset the cache

Warning: deleting the cache PVC removes `ccache`, `uv`, and Go caches for all
builds that use it.

```bash
zeta volumes --role cache
kubectl --context local -n zeta-poc delete job \
  -l zeta.io/managed-by=portal --ignore-not-found
kubectl --context local -n zeta-poc delete pvc zeta-poc-cache
```

The PVC is recreated on the next build. Profiles with a dedicated `cachePVC`
keep their own volume.

### 11.8 Reset artifacts

```bash
zeta volumes --role artifacts
kubectl --context local -n zeta-poc delete pvc zeta-poc-artifacts
```

The shared PVC is recreated on the next build. Profiles with a dedicated
`artifactsPVC` keep their own volume.

---

## 12. Troubleshooting

### `The connection to the server 127.0.0.1:6443 was refused`

k3s is not running. Restart it:

```bash
sudo -n bash -c 'nohup k3s server >/tmp/k3s-server.log 2>&1 &'
kubectl --context local get nodes
```

### `zeta: session absente; lancez zeta login`

The CLI has no local session. Run `zeta login`.

### `non authentifié` or `session expirée`

The cookie is invalid or expired. Run `zeta login` again. If the portal restarted
without a fixed `--oidc-session-key`, all sessions were invalidated.

### `URL de dépôt invalide`

Use `git://`, `http://`, `https://`, or `ssh://`. `file://` URLs and local paths
are rejected.

### `référence Git invalide`

The commit contains forbidden characters (`..`, spaces, `;`, `$`, etc.). Use a
SHA, tag, or simple branch name.

### `remote HEAD refers to nonexistent ref`

The bare repository points at a missing branch. Fix it:

```bash
kubectl --context local -n zeta-poc exec deploy/git-server -- \
  sh -c 'git --git-dir=/srv/git/cpp.git symbolic-ref HEAD refs/heads/main'
```

### `dubious ownership` in a Job

The Job already configures `safe.directory /workspace`. If a custom profile
clones elsewhere, add the same Git configuration.

### `ImagePullBackOff` / `ErrImageNeverPull`

The image is not present in k3s containerd. Re-import it:

```bash
docker save zeta/go:0.1 | sudo -n k3s ctr images import -
```

Or rebuild all images:

```bash
cd platform
make images
```

### Build stuck in `Pending`

Check PVCs and the StorageClass:

```bash
kubectl --context local -n zeta-poc get pvc
kubectl --context local get storageclass
```

The reference k3s setup uses `local-path`.

### `PodInitializing` in logs

Context extraction (tar or Git clone) is still running. Wait or inspect the init
container logs:

```bash
kubectl --context local -n zeta-poc logs <pod> -c context
```

### Ingress 404

The `Host` header does not match `zeta.local`. Check the hosts entry or use
a port-forward.

### Port-forward not reachable from Windows

Use `--address 0.0.0.0`:

```bash
kubectl --context local -n zeta-poc port-forward \
  --address 0.0.0.0 svc/zeta-portal 8080:80
```

### Builds disappear after one hour

This is the Job TTL (`TTLSecondsAfterFinished: 3600`). For longer retention,
change `portal/internal/kube/repository.go` and redeploy.

### Artifact download fails with `unexpected EOF`

Artifacts are streamed as gzip+base64 through pod logs to avoid binary log
corruption. If this still fails, check the portal logs for the artifact error and
verify the build Job succeeded:

```bash
kubectl --context local -n zeta-poc logs deploy/zeta-portal --tail=100
zeta status <id>
```

---

## 13. Security model and limitations

Security model:

- Developers never receive a kubeconfig.
- The portal uses a namespace-scoped ServiceAccount.
- Jobs run as non-root `1000:1000` with no service account token.
- PSA `restricted`, seccomp `RuntimeDefault`, capabilities dropped.
- Portal root filesystem is read-only.
- Build context archives are validated against path traversal, out-of-context
  links, and special files.
- Sync paths are validated and dangerous symlinks are rejected.

Current limitations:

- No full per-group RBAC or multi-tenant isolation. Builds and environments are
  owner-scoped for lifecycle, exec, sync, logs, metrics, and artifacts; listing
  remains shared. Volumes remain shared.
- No IdP logout/revocation; the signed session cookie remains valid until expiry
  after local logout.
- OIDC browser login is not integrated into the CLI yet.
- No private SSH repository support; use HTTPS tokens.
- Artifacts are stored per language path, not per build. A newer build of the
  same profile can overwrite the previous artifact directory.
- No S3/MinIO backend; PVCs use `local-path` on the reference single-node cluster.
- No automatic volume garbage collection. `zeta volumes --older-than` only reports
  candidates; deletion is manual from the CLI or UI.
- Volume expansion requires a StorageClass with `allowVolumeExpansion: true`.
  The k3s `local-path` class does not support it; use an EBS-backed class in
  production.
- Jobs and their artifacts expire after one hour by default.
- No `.dockerignore`; use `--exclude` and the default context exclusions.
- Build contexts are limited to 512 MiB uploaded and 2 GiB uncompressed.

---

## 14. Development

Run tests and vet:

```bash
cd portal
go test ./...
go vet ./...

cd ../cli
go test ./...
go vet ./...
```

Build the portal image and deploy:

```bash
cd portal
make test
make deploy
```

Build the CLI locally:

```bash
cd cli
make build
./bin/zeta help
```

Build and import all build images:

```bash
cd platform
make images
```

Useful Makefile targets:

```bash
make doctor
make install-smart          # build images, import into k3s, Helm install
make install-platform       # raw manifests (development)
make install-platform WITH_GIT_SERVER=1
make install-user           # build the CLI from source
make install-cli            # install the latest release binary
make install                # install-smart + install-user
make status
make uninstall-platform
make helm-lint
make helm-template
make helm-package
```

Development targets:

```bash
cd platform
make images
make git-init
make cpp
make python
make go
make all
make git-all
make cache-reset
make clean
```

---

## 15. Repository layout

```text
.
├── Makefile
├── README.md
├── mkdocs.yml
├── requirements-docs.txt
├── .github/workflows/
│   ├── docs.yml
│   └── release.yml
├── charts/
│   └── zeta/
│       ├── Chart.yaml
│       ├── values.yaml
│       └── templates/
├── scripts/
│   ├── doctor.sh
│   ├── install.sh
│   ├── install-cli.sh
│   ├── install-cli.ps1
│   ├── install-platform.sh
│   ├── install-user.sh
│   └── uninstall-platform.sh
├── docs/
│   ├── index.md
│   ├── platform/
│   ├── developer/
│   ├── getting-started/
│   ├── user-guide/
│   ├── reference/
│   ├── operations/
│   └── development/
├── design/
│   └── cadrage.md
├── platform/
│   ├── Makefile
│   ├── examples/
│   │   ├── cpp/
│   │   ├── go/
│   │   └── python-uv/
│   ├── images/
│   │   ├── cpp/
│   │   ├── go/
│   │   ├── python-uv/
│   │   └── git-server/
│   ├── manifests/
│   │   ├── namespace.yaml
│   │   ├── pvc.yaml
│   │   ├── cache-pvc.yaml
│   │   ├── context-pvc.yaml
│   │   ├── git-server.yaml
│   │   ├── job-cpp.yaml
│   │   ├── job-cpp-git.yaml
│   │   ├── job-python.yaml
│   │   ├── job-python-git.yaml
│   │   ├── job-go.yaml
│   │   ├── job-go-git.yaml
│   │   └── portal.yaml
│   └── scripts/
│       ├── build-images.sh
│       ├── init-git.sh
│       ├── run-build.sh
│       └── benchmark.sh
├── portal/
│   ├── cmd/zeta/main.go
│   ├── internal/
│   │   ├── auth/
│   │   ├── builds/
│   │   ├── config/
│   │   ├── domain/
│   │   ├── httpapi/
│   │   ├── kube/
│   │   ├── sync/
│   │   └── ui/
│   ├── Dockerfile
│   └── Makefile
└── cli/
    ├── cmd/zeta/main.go
    ├── cmd/syncd/main.go
    ├── internal/
    │   ├── client/
    │   ├── config/
    │   ├── contexttar/
    │   ├── gitutil/
    │   └── sync/
    └── Makefile
```
