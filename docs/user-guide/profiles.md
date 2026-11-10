# Build profiles

A profile describes how to build one language: the image, the commands, the
artifact path, the cache, the resources, the environment, and the metrics.

Profiles are loaded from `portal/internal/builds/defaults.yaml` and merged with
the optional `--profiles-file` / `ZETA_PROFILES_FILE` file. Merging is by name: a
custom profile can replace `cpp`, `python`, or `go`, or add a new one.

## Built-in profiles

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

## Registry and tag overrides

Profiles in the defaults use short image names such as `zeta/cpp:0.1`. The
portal can rewrite them at startup:

```bash
zeta-portal -image-registry=ghcr.io/slimaineb -image-tag=0.1.0
```

or with environment variables:

```bash
ZETA_IMAGE_REGISTRY=ghcr.io/slimaineb ZETA_IMAGE_TAG=0.1.0 zeta-portal
```

The override applies to every profile image and to the helper images used by
builds (context init, `syncd`, and the optional Git server). Images that already
include a registry are left unchanged. The Helm chart wires this automatically
from `image.registry` / `image.tag`.

See [Portal configuration](../reference/configuration.md#image-overrides).

## Schema

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

### Fields

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
| `cachePVC` | no | Dedicated cache PVC; defaults to the shared cache PVC |
| `cacheSize` | no | Size used when creating `cachePVC`; defaults to `2Gi` |
| `artifactsPVC` | no | Dedicated artifacts PVC; defaults to the shared artifacts PVC |
| `artifactsSize` | no | Size used when creating `artifactsPVC`; defaults to `1Gi` |
| `storageClass` | no | StorageClass for dedicated PVCs; defaults to the portal `--storage-class` |
| `artifactExclude` | no | Glob patterns excluded from artifact download and sync |
| `maxConcurrent` | no | Maximum concurrent builds for this profile; defaults to `1` |
| `env` | no | Literal environment variables injected into `context` and `build` |
| `envFromSecrets` | no | Optional Secrets injected with `envFrom` |
| `resources` | no | CPU, memory, and ephemeral-storage requests/limits |
| `metrics` | no | Metric commands run after the build |

## Environment variables and Secrets

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

## Metrics

After the build commands, the portal runs the `metrics` commands with `set +e`.
Each line in this format is parsed:

```text
ZETA_METRIC name=value
```

Known metrics:

- `ccache_hits`
- `ccache_misses`
- `cache_bytes`

Other metrics are kept in `extra` and displayed as chips.

## Custom profiles in Kubernetes

Create a ConfigMap:

```bash
kubectl --context local -n zeta-poc create configmap zeta-profiles \
  --from-file=profiles.yaml=./profiles.yaml
```

Add a volume and the `-profiles-file=/etc/zeta/profiles.yaml` argument to the
Deployment:

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

## Adding a new profile

1. Build a container image with the toolchain.
2. Import it into k3s (`docker save ... | sudo -n k3s ctr images import -`).
3. Add a profile entry to `portal/internal/builds/defaults.yaml` or a custom
   profiles file.
4. Redeploy the portal if you changed `defaults.yaml`.
5. Verify with `zeta profiles` and a small build.

## Concurrency and artifacts

Each profile defaults to `maxConcurrent: 1`. Builds of the same profile are
serialized because they write to the same `artifactPath`. Different profiles can
run in parallel.

Set `maxConcurrent` above `1` only when each build writes to a unique
`artifactPath`; otherwise concurrent builds can overwrite each other's
artifacts.

To keep large or sensitive files out of downloads, configure
`artifactExclude`:

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

## Volume strategy

By default all profiles share these PVCs:

| Role | Default PVC | Mount | Contents |
|---|---|---|---|
| `cache` | `zeta-poc-cache` | `/cache` | Compiler, package, and module caches |
| `artifacts` | `zeta-poc-artifacts` | `/artifacts` | Build outputs |
| `context` | `zeta-poc-contexts` | `/contexts` | Uploaded local contexts |

Set `cachePVC` / `artifactsPVC` to give a profile dedicated volumes. A dedicated
volume is created on first use with the requested size and `storageClass`, then
reused. Existing PVCs are never resized or recreated automatically.

See [Volumes and artifacts](volumes.md).
