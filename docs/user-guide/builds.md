# Builds

A build is an ephemeral Kubernetes Job created by the portal. The source comes
either from your local working tree or from a pushed Git commit. The build runs
in the profile image with a persistent cache and an artifacts volume.

## Two modes

| Mode | Trigger | Source | Use case |
|---|---|---|---|
| Local context | `zeta build` | `tar.gz` of the local directory | Experiments, private branches, monorepos |
| Git | `zeta build --git` or `--repo` | Remote commit | Reproducible, auditable builds |

Local context is the default: no remote, webhook, or pushed commit is required.

## Profile detection

If `--profile` is empty, the CLI detects the language:

| File present | Language | Profile |
|---|---|---|
| `CMakeLists.txt` | `cpp` | `cpp` |
| `go.mod` | `go` | `go` |
| `pyproject.toml`, `setup.py`, `requirements.txt` | `python` | `python` |

If no profile matches, pass `--profile` with one of the names returned by
`zeta profiles`.

## Local context

```bash
cd my-project
zeta build
```

The CLI creates a filtered `tar.gz`. Default exclusions are:

```text
.git
.venv
build
dist
__pycache__
node_modules
.pytest_cache
.mypy_cache
.ruff_cache
.tox
.cache
.zeta
.zeta-ready
```

Add exclusions with `--exclude` (repeatable):

```bash
zeta build --exclude .cache --exclude docs
```

The artifact directory (`--artifacts-dir`, `artifacts` by default) is also
excluded from the context.

### Context limits

- Upload limited to **512 MiB**.
- Uncompressed size limited to **2 GiB**.
- Absolute paths, `..`, out-of-context links, and special files are rejected.
- There is no `.dockerignore`: use `--exclude` and the default exclusions.

## Git mode

```bash
zeta build --git --profile cpp \
  --repo git://git-server/cpp.git \
  --commit main
```

- `--repo` enables Git mode; `--git` alone uses the current repository remote.
- `--commit` accepts a branch, tag, `HEAD`, or SHA. Empty uses the current
  branch.
- Accepted URL schemes are `git://`, `http://`, `https://`, and `ssh://`.
- `file://` URLs and local paths are rejected.
- Shell metacharacters and `..` are rejected in Git references.
- A local unpushed commit is not visible from the cluster.

With the example Git server, the in-cluster URL is
`git://git-server/<repo>.git`. To clone locally, use a port-forward:

```bash
kubectl --context local -n zeta-poc port-forward svc/git-server 19418:9418
git clone git://127.0.0.1:19418/cpp.git
```

The `127.0.0.1:19418` URL is not reachable from the cluster: for builds, use
`git://git-server/cpp.git`.

## Sync during a build

```bash
zeta build --sync
```

Sync mode adds a `syncd` sidecar and a WebSocket relay:

```text
zeta CLI <--- WebSocket ---> portal sync hub <--- WebSocket ---> syncd
                                                                   |
                                                             /workspace
                                                             /artifacts
```

Flow:

1. The initial context is synchronized before the build starts.
2. The build waits for the `/workspace/.zeta-ready` marker.
3. Local edits are sent to the pod while the build runs.
4. Files created in `/workspace` and artifacts are sent back.
5. A final flush writes the latest artifacts to `artifacts/<id>`.

Default exclusions prevent local outputs (`build`, `dist`, `.venv`, etc.) from
being overwritten. `--sync` is incompatible with Git.

The build timeout is also the sync timeout (`--timeout`, `30m` by default).

## Follow a build

```bash
zeta builds
zeta status zeta-go-xxxxxxxxxxxx
zeta logs zeta-go-xxxxxxxxxxxx
zeta metrics zeta-go-xxxxxxxxxxxx
zeta artifacts -o artifacts.tar zeta-go-xxxxxxxxxxxx
```

Possible statuses:

| Status | Meaning |
|---|---|
| `pending` | Job created, pod not started yet |
| `running` | Build in progress |
| `succeeded` | Build succeeded |
| `failed` | Build failed |
| `unknown` | State could not be determined |

## Artifacts

The exported directory is the profile's `artifactPath`:

| Profile | Directory |
|---|---|
| `cpp` | `/artifacts/cpp` |
| `python` | `/artifacts/python` |
| `go` | `/artifacts/go` |

`zeta build` downloads and extracts artifacts by default. To fetch them later:

```bash
zeta artifacts -o build.tar zeta-go-xxxxxxxxxxxx
```

The archive contains the contents of `artifactPath`. Artifacts are stored per
language, not per build: a newer build of the same profile can overwrite the
previous artifact directory on the shared PVC.

### Artifact exclusions

Configure `artifactExclude` in the profile, or use the flag:

```bash
zeta build --artifact-exclude '*.log' --artifact-exclude 'coverage/'
```

Patterns are passed to `tar --exclude` on the portal, applied during local
extraction, and ignored by the sync client.

## Concurrency

Each profile defaults to `maxConcurrent: 1`. Builds of the same profile are
serialized because they write to the same `artifactPath`. Different profiles can
run in parallel.

Increase `maxConcurrent` only when each build writes to a unique
`artifactPath`; otherwise artifacts can overwrite each other.

## Lifetime

- Jobs have a **1 hour** TTL (`TTLSecondsAfterFinished: 3600`).
- `ActiveDeadlineSeconds` is **3600** for a normal build and **1800** for a sync
  build.
- A stuck build therefore cannot run forever.
- Jobs and their logs disappear from the cluster and UI after the TTL.

Manual deletion:

```bash
kubectl --context local -n zeta-poc delete job \
  -l zeta.io/managed-by=portal
```

## Best practices

- Use `--sync` to debug without rebuilding the environment manually.
- Use Git mode for reproducible, shareable builds.
- Keep caches on PVCs; do not put them in the local context.
- Add `--exclude` for large generated directories.
- Check `zeta volumes` before deleting a cache.
