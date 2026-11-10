# `zeta.yaml` reference

`zeta.yaml` configures **persistent environments**. It is read by `zeta init`,
`zeta activate`, `zeta deactivate`, and `zeta rm`. Builds (`zeta build`) do not
read it: they use flags, `ZETA_PROFILE`, and language auto-detection.

## Discovery and precedence

- The file is read from the `--context` directory (default `.`).
- `context` is resolved relative to the directory containing `zeta.yaml`.
- CLI flags override file values.
- The environment name falls back to the context directory name.
- The StorageClass is a **platform** setting (`--storage-class` /
  `ZETA_STORAGE_CLASS`), not a project setting.

## Schema

```yaml
name: dev
profile: go
image: zeta/go:0.1
context: .
size: 5Gi
exclude:
  - artifacts
  - .cache
env:
  GOFLAGS: -mod=mod
  APP_ENV: development
  PORT: 8080
  DEBUG: true
```

| Field | Type | Default | Description |
|---|---|---|---|
| `name` | string | directory name | Environment name (normalized to a slug) |
| `profile` | string | auto-detected | Build profile used for the image, cache, and resources |
| `image` | string | profile image | Base image override for this environment |
| `context` | string | `.` | Local directory mirrored into `/workspace` |
| `size` | string | `5Gi` | Workspace PVC size |
| `exclude` | list of strings | `[]` | Extra sync exclusions |
| `env` | mapping | `{}` | Extra environment variables |

All fields are optional.

## `env` values

`env` accepts any scalar YAML value and stores it as a string, so these are
equivalent:

```yaml
env:
  PORT: 8080
  DEBUG: true
  MODE: development
```

The environment receives:

```text
PORT=8080
DEBUG=true
MODE=development
```

Project values override profile values with the same name.

## Context resolution

If `context` is relative, it is joined to the directory containing
`zeta.yaml`:

```yaml
# /home/user/project/zeta.yaml
context: ./src
```

The mirrored root is `/home/user/project/src`.

If `context` is absolute, it is used as-is.

## Image override

`image` replaces the profile image for this environment only. The profile still
provides the cache PVC, resources, and environment defaults.

```yaml
profile: go
image: zeta/python-uv:0.1
```

Useful for a one-off toolchain without creating a new profile.

## Exclusions

`exclude` adds to the environment sync exclusions. The defaults already exclude
`.git`, `.venv`, `__pycache__`, `node_modules`, and tool caches. `build/` and
`dist/` are intentionally **not** excluded so outputs come back locally.

```yaml
exclude:
  - artifacts
  - tmp
  - "*.log"
```

## Name normalization

The name is lowercased. Characters other than `a-z`, `0-9`, and `-` become `-`.
Leading and trailing dashes are trimmed, and the result is limited to 40
characters. An empty result becomes `env`.

Examples:

| Input | Result |
|---|---|
| `My Project` | `my-project` |
| `api_v2` | `api-v2` |
| `--` | `env` |

## Complete examples

### Go

```yaml
name: go-dev
profile: go
size: 5Gi
env:
  GOFLAGS: -mod=mod
```

### Python

```yaml
name: py-dev
profile: python
size: 8Gi
env:
  UV_LINK_MODE: copy
```

### C++

```yaml
name: cpp-dev
profile: cpp
size: 10Gi
exclude:
  - build
  - cmake-build-debug
env:
  CMAKE_BUILD_TYPE: Debug
```

## Interaction with commands

| Command | Reads `zeta.yaml` | Uses |
|---|---|---|
| `zeta init` | yes | `name`, `profile`, `image`, `size`, `env` |
| `zeta activate` | yes | `name`, `context`, `exclude` |
| `zeta deactivate` | yes | `name`, `context` |
| `zeta rm` | yes | `name`, `context` |
| `zeta build` | no | flags, `ZETA_PROFILE`, auto-detection |

## Validation

- `env` must be a mapping.
- `size` is passed to Kubernetes; invalid values are rejected by the API server
  or the portal.
- Unknown fields are ignored by the YAML decoder.
