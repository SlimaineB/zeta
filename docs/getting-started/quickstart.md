# First build

This guide assumes the portal is already installed. If not, follow
[Install the platform](../platform/installation.md) and
[Install the CLI](../developer/installation.md).

## 1. Log in

```bash
zeta login --server http://zeta.local --email dev@example.com --password dev
```

The session is stored in `~/.config/zeta/config.json` with mode `0600`.

## 2. Check profiles

```bash
zeta profiles
```

```text
NAME    LANGUAGE  DEFAULT REPO                     DESCRIPTION
cpp     cpp       git://git-server/cpp.git         C++ CMake/Ninja with persistent ccache
go      go        git://git-server/go.git          Go modules with persistent GOCACHE/GOMODCACHE
python  python    git://git-server/python-uv.git   Python 3.12 + uv with persistent uv cache
```

## 3. Run a local build

From a project directory:

```bash
cd my-project
zeta build
```

The CLI detects the profile from the files present:

| Detected file | Profile |
|---|---|
| `CMakeLists.txt` | `cpp` |
| `go.mod` | `go` |
| `pyproject.toml`, `setup.py`, `requirements.txt` | `python` |

It packages the context as a `tar.gz`, uploads it, streams logs, waits for
completion, and extracts artifacts into `artifacts/<id>`.

Example output:

```text
context: /home/user/my-project (27 KB)
build zeta-cpp-xxxxxxxxxxxx (cpp) context /home/user/my-project
--- logs ---
[ live build logs ]
build succeeded in 5.0 s
cache: 7 hits / 1 miss (88%) · 43 KB
artifacts: /home/user/my-project/artifacts/zeta-cpp-xxxxxxxxxxxx
```

## 4. Force a profile or adjust the build

```bash
# Force the profile
zeta build --profile go

# Exclude files from the context
zeta build --exclude .cache --exclude docs

# Do not follow logs
zeta build --follow=false

# Do not download artifacts
zeta build --artifacts=false

# Choose the output directory
zeta build --artifacts-dir ./out

# Timeout (30m by default)
zeta build --timeout 10m
```

## 5. Build from Git

Git mode builds a commit that is already pushed and reachable from the cluster:

```bash
zeta build --git --profile go \
  --repo git://git-server/go.git \
  --commit main
```

With the example Git server, the in-cluster URL is
`git://git-server/<repo>.git`.

## 6. Interactive sync

To edit locally during the build and recover files produced inside the pod:

```bash
zeta build --sync
```

The build waits for the `/workspace/.zeta-ready` marker, syncs the initial
context, then exchanges changes in both directions. `--sync` is incompatible
with Git mode.

## 7. Persistent environment

An environment is a pod that stays available between commands, backed by a
persistent volume and a bidirectional mirror of your local directory.

```bash
cd my-project
zeta init                 # create the volume, secret, and stopped deployment
zeta activate             # start the pod, open a shell, and sync files
zeta ls                   # list environments
zeta deactivate           # scale the pod to zero, keep the volume
zeta rm                   # delete everything, including the volume
```

A `zeta.yaml` file at the project root avoids repeating options:

```yaml
name: my-project
profile: go
image: zeta/go:0.1
size: 5Gi
env:
  GOFLAGS: -mod=mod
```

See [Persistent environments](../user-guide/environments.md) and
[`zeta.yaml` reference](../reference/zeta-yaml.md).

## Next steps

- [CLI guide](../user-guide/cli.md)
- [Builds and sync](../user-guide/builds.md)
- [Build profiles](../user-guide/profiles.md)
- [Volumes and artifacts](../user-guide/volumes.md)
