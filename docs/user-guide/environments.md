# Persistent environments

A build is disposable. An **environment** is a long-lived pod backed by a
persistent volume and kept in sync with your local directory. It stays available
between commands, so you can run `go build`, `make`, tests, or a debugger without
waiting for a new pod each time.

## Lifecycle

```bash
zeta init        # create the volume, sync secret, and stopped deployment
zeta activate    # start the pod, open a shell in /workspace, mirror files
zeta ls          # list environments
zeta deactivate  # scale the pod to zero, keep the volume
zeta rm          # delete everything, including the volume
```

| Command | Effect |
|---|---|
| `zeta init` | Creates PVC `zeta-env-<id>`, Secret `zeta-env-<id>-sync`, and Deployment `zeta-env-<id>` at 0 replicas |
| `zeta activate` | Scales to 1, starts the sync client, and opens an interactive shell |
| `zeta deactivate` | Scales to 0 and drops the sync session |
| `zeta rm` | Deletes the Deployment, pods, Secret, and PVC |

`zeta up` / `zeta down` are aliases for `activate` / `deactivate`.

## Naming

The environment name is resolved from:

1. `--name`;
2. the `name` field in `zeta.yaml`;
3. the context directory name.

It is normalized to a slug: lowercase letters, digits, and dashes, up to 40
characters.

## `zeta.yaml`

Most options live in `zeta.yaml` at the project root:

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

`zeta.yaml` is read from the `--context` directory (default `.`), and `context`
is resolved relative to it. CLI flags override the file.

See [`zeta.yaml` reference](../reference/zeta-yaml.md).

## What gets mirrored

The environment uses a different exclusion set from builds: dependencies and
caches stay remote, but build outputs are synced back.

Excluded from the mirror:

```text
.git
.venv
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

`build/` and `dist/` are **not** excluded, so outputs produced inside the pod
come back to your machine. Add extra exclusions with `--exclude` (repeatable) or
the `exclude` list in `zeta.yaml`.

## The shell

`zeta activate` starts the pod, waits for the initial sync, and opens a shell in
`/workspace`. The shell is interactive and non-login:

```sh
sh -c "cd /workspace && (command -v bash >/dev/null && exec bash -i || exec sh -i)"
```

Leaving the shell (`Ctrl-D` or `exit`) does **not** stop the environment. Run
`zeta deactivate` explicitly when you are done, or let the idle reaper stop it.

## Idle scale-down

To save compute, the portal scales an environment back to zero when no shell or
sync client has been attached for `--env-idle-timeout`:

- default: `15m`;
- `0` disables the reaper;
- the reaper checks at most once per minute;
- the volume and its contents are preserved;
- the next `zeta activate` resumes quickly.

The portal logs a line such as:

```text
env reaper: dev scaled to zero after 15m0s idle
```

## Storage

| Item | Default |
|---|---|
| Workspace PVC | `zeta-env-<id>`, `5Gi` |
| StorageClass | platform `--storage-class` (default `local-path`) |
| Cache | shared profile cache PVC (default `zeta-poc-cache`, `2Gi`) |

The workspace PVC is mounted at `/workspace`; the cache is mounted at `/cache`.
The volume is labeled `zeta.io/volume-role=env` and kept across
`deactivate`/`activate`.

## Image and environment overrides

`image` overrides the profile base image for this environment. `env` is merged
with the profile environment; project values win by name.

Example: profile `go` with a Python image for a one-off environment:

```yaml
name: py-playground
profile: go
image: zeta/python-uv:0.1
env:
  CUSTOM: "yes"
```

The deployment uses `zeta/python-uv:0.1` and keeps the profile's cache and
resource settings.

## Deletion and timeouts

`zeta rm` deletes the Deployment, its pods, the sync Secret, and the PVC. The
portal waits up to 45 seconds for pods and 60 seconds for the PVC. If the volume
is still terminating, the request fails with:

```text
environment <id> was removed but its volume is still terminating
```

Retry after the volume finalizer completes.

## Limitations

- Environments share the `zeta-poc` namespace and the portal RBAC. Environments
  are owner-scoped for lifecycle, exec, and sync operations; `admin` / `admins`
  group members can manage any environment. There is no full per-group
  isolation yet.
- Sync is last-writer-wins; simultaneous edits on both sides can conflict.
- `local-path` volumes are node-local. Use an EBS-backed StorageClass in
  production.
- The workspace is mirrored, not mounted over the network. Large trees take
  longer to sync initially.
- The sync sidecar uses `zeta/portal:0.1`; keep that image available in
  containerd.

## Troubleshooting

| Symptom | Check |
|---|---|
| `activate` hangs | `kubectl -n zeta-poc get pods`, `kubectl -n zeta-poc logs deploy/zeta-env-<id> -c syncd` |
| Shell exits immediately | Check the `env` container logs; the image must have a shell |
| Files do not come back | Check `exclude` and the `build/`, `dist/` paths |
| Environment stops by itself | Idle timeout; run `zeta activate` again |
| `rm` returns 504 | The portal restarted or the PVC is terminating; retry |
| Pod stays `Pending` | Check the PVC and StorageClass |

See [Troubleshooting](../operations/troubleshooting.md) for more.
