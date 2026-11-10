# Repository layout

```text
.
├── Makefile
├── README.md
├── mkdocs.yml
├── requirements-docs.txt
├── .github/workflows/docs.yml
├── scripts/
│   ├── doctor.sh
│   ├── install-platform.sh
│   ├── install-user.sh
│   └── uninstall-platform.sh
├── docs/
│   ├── index.md
│   ├── getting-started/
│   ├── user-guide/
│   ├── reference/
│   ├── operations/
│   └── development/
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
│   │   ├── job-*.yaml
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

## Portal packages

| Package | Responsibility |
|---|---|
| `cmd/zeta` | Portal entrypoint, flags, HTTP server, idle reaper |
| `internal/auth` | Dev login, OIDC, sessions, middleware |
| `internal/builds` | Profile loading/merging, build and environment service |
| `internal/config` | Flags and environment variables |
| `internal/domain` | Shared types and repository interfaces |
| `internal/httpapi` | Routes, handlers, exec proxy, sync WebSockets, UI |
| `internal/kube` | Kubernetes repository: Jobs, Deployments, PVCs, Secrets, exec |
| `internal/sync` | In-memory sync hub, tokens, activity tracking |
| `internal/ui` | Embedded dashboard assets |

## CLI packages

| Package | Responsibility |
|---|---|
| `cmd/zeta` | Command dispatch and output |
| `cmd/syncd` | Sync sidecar entrypoint for build and environment pods |
| `internal/client` | HTTP client, API types, cookies |
| `internal/config` | Local session file |
| `internal/contexttar` | Context archive creation and exclusions |
| `internal/gitutil` | Git remote, branch, and dirty checks |
| `internal/sync` | Sync client, exec WebSocket, PTY shell |

## Where to change what

| Task | Files |
|---|---|
| Add or change a CLI command | `cli/cmd/zeta/main.go` |
| Add an API route | `portal/internal/httpapi/server.go` |
| Change build or environment resources | `portal/internal/kube/repository.go` |
| Change profile defaults | `portal/internal/builds/defaults.yaml` |
| Change flags or env variables | `portal/internal/config/config.go` |
| Change the dashboard | `portal/internal/ui/` |
| Change manifests or RBAC | `platform/manifests/` |
| Change images | `platform/images/` and `platform/scripts/build-images.sh` |
| Change docs | `docs/` and `mkdocs.yml` |

## Data flow

```text
CLI ──► client ──► HTTP API ──► builds.Service ──► kube.Repository ──► Kubernetes
                    │                 │
                    │                 └── sync.Hub ◄── syncd (WebSocket)
                    └── ui / static
```

- `httpapi` validates HTTP input and maps errors to status codes.
- `builds` resolves profiles and orchestrates builds/environments.
- `kube` is the only package that talks to the Kubernetes API.
- `sync` keeps WebSocket sessions and activity timestamps in memory.
