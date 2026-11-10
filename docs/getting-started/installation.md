# Installation

Zeta has two installation paths:

- **Platform team:** install the portal, RBAC, and PVCs with Helm.
  See [Install the platform](../platform/installation.md).
- **Developers:** install the `zeta` CLI with `curl | sh` or PowerShell.
  See [Install the CLI](../developer/installation.md).

For a local k3s cluster without a registry, use the source installer:

```bash
./scripts/install.sh
```

See [Platform overview](../platform/index.md#install-from-source) for the local
build path, and [First build](quickstart.md) after installation.
