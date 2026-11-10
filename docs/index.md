# Zeta

**Zeta** is a Kubernetes build portal for developers **without user kubeconfigs**.
Developers submit builds from their workstation with the `zeta` CLI. The portal
runs inside the cluster, creates Kubernetes Jobs with its own ServiceAccount, and
streams logs, metrics, artifacts, and optional bidirectional workspace sync back
to the CLI.

The web UI is a monitoring dashboard, not a build form.

<div class="grid cards" markdown>

-   :material-rocket-launch:{ .lg .middle } **Get started**

    ---

    Install the portal and CLI, then run your first build in minutes.

    [:octicons-arrow-right-24: Installation](getting-started/installation.md)

-   :material-console:{ .lg .middle } **Use the CLI**

    ---

    Local or Git builds, live logs, artifacts, volumes, and environments.

    [:octicons-arrow-right-24: CLI guide](user-guide/cli.md)

-   :material-server-network:{ .lg .middle } **Persistent environments**

    ---

    A long-lived pod, a volume, an interactive shell, and file mirroring.

    [:octicons-arrow-right-24: Environments](user-guide/environments.md)

-   :material-sitemap:{ .lg .middle } **Understand the architecture**

    ---

    Control plane, execution plane, Jobs, PVCs, and the sync hub.

    [:octicons-arrow-right-24: Architecture](reference/architecture.md)

</div>

## What Zeta gives you

- **Zero Kubernetes onboarding.** No kubeconfig, no namespace access, no
  `kubectl` knowledge required.
- **Reproducible environments.** Every build uses a pinned image and profile, so
  local machine differences do not affect the result.
- **Isolation.** Builds run as non-root Jobs under `restricted` Pod Security,
  without service account tokens.
- **Fast feedback.** Logs stream live, caches persist between builds, and
  artifacts are downloaded automatically.
- **Flexible source input.** Use the local working tree by default, or a pushed
  Git commit when you need an auditable revision.
- **Interactive debugging.** `--sync` mirrors your local code into the build pod
  and brings produced files back.
- **Persistent dev environments.** `zeta activate` opens a shell in a pod that
  stays available between commands, backed by a persistent volume.
- **Extensible.** A new language is a YAML profile plus an image, not a control
  plane change.

## High-level view

```text
Developer workstation
  |
  |  zeta build / zeta activate
  v
zeta CLI ---- HTTP + session cookie ----> Zeta portal (in-cluster)
                                          |
                                          | ServiceAccount + namespace RBAC
                                          v
                                    Kubernetes Job (build)
                                      |- init: extract context or clone Git
                                      |- build: run profile commands
                                      |- syncd: optional workspace sync
                                      |- cache PVC
                                      `- artifacts PVC
                                          |
                                          v
                               Portal: logs, status, metrics, artifacts
                                          |
                                          v
                                   Web dashboard
```

For persistent environments, the workload is not an ephemeral Job:

```text
zeta CLI  <--- WebSocket --->  portal sync hub  <--- WebSocket --->  syncd
       |                                                                |
       |  interactive shell (exec)                                      v
       `------------------------------------------------------>  /workspace (PVC)
```

## What Zeta is not

- It is not a full CI/CD system with SCM webhooks, pipelines, approvals, and
  deployment stages.
- It does not provide per-group RBAC or strong multi-tenant isolation yet.
- It does not build or push OCI images; it produces build artifacts on a PVC.
- It is not a remote cache or a distributed build system for very large
  monorepos.

## Project status

Zeta is a working proof of concept validated on a single-node k3s cluster
(WSL2). The following are implemented and tested:

- local and Git builds, C++/Python/Go profiles, persistent caches;
- artifacts, metrics, web dashboard, and HTTP API;
- persistent environments with shell, file sync, and idle scale-down;
- volume quotas, resize, and deletion;
- dev and OIDC authentication.

Known limitations are documented in [Security](operations/security.md) and
[Troubleshooting](operations/troubleshooting.md).

## Documentation

- [Installation](getting-started/installation.md)
- [First build](getting-started/quickstart.md)
- [`zeta.yaml` reference](reference/zeta-yaml.md)
- [HTTP API](reference/api.md)
