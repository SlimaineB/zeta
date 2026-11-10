# Volumes and artifacts

Zeta separates three storage roles. PVCs are mounted only while a build pod or an
environment pod is running; after the workload stops, only the storage remains.
This is the same cost model as an EBS volume attached during a build and detached
afterwards.

## Roles

| Role | Default PVC | Mount | Contents |
|---|---|---|---|
| `cache` | `zeta-poc-cache` | `/cache` | Compiler, package, and module caches |
| `artifacts` | `zeta-poc-artifacts` | `/artifacts` | Build outputs |
| `context` | `zeta-poc-contexts` | `/contexts` | Uploaded local contexts |
| `env` | `zeta-env-<id>` | `/workspace` | Persistent environment workspace |

By default all profiles share the cache, artifacts, and context PVCs. Set
`cachePVC` / `artifactsPVC` in a profile to give it dedicated volumes instead. A
dedicated volume is created on first use with the requested size and
`storageClass`, then reused on later builds. Existing PVCs are never resized or
recreated automatically.

## Labels and annotations

The portal labels managed PVCs with:

- `zeta.io/managed-by=portal`
- `zeta.io/volume-role`

It records last use in annotations:

- `zeta.io/last-used-at`
- `zeta.io/last-build`
- `zeta.io/last-profile`
- `zeta.io/last-owner`

Environment PVCs are additionally labeled with `zeta.io/env=<id>`.

## List volumes

```bash
zeta volumes
```

```text
NAME                 ROLE       SIZE    LAST USE                       BUILD
zeta-poc-artifacts   artifacts  1.0 GB  2026-11-08 18:59 (just now)   zeta-go-8415dfd8487a
zeta-poc-cache       cache      2.0 GB  2026-11-08 18:59 (just now)   zeta-go-8415dfd8487a
zeta-poc-contexts    context    2.0 GB  2026-11-08 18:59 (just now)   zeta-go-8415dfd8487a
```

Filters:

```bash
zeta volumes --role cache
zeta volumes --older-than 720h
zeta volumes --json
```

`--older-than` keeps volumes whose last build is older than the given duration.
It is a **reporting** filter: Zeta does not delete volumes automatically.

The quota is displayed above the volume table:

```text
quota: 5.0 GiB / 20 GiB used, 15 GiB free (max per volume: 10 GiB)
```

## Resize or delete

```bash
zeta volumes resize zeta-poc-cache 5Gi
zeta volumes delete zeta-test-cache
```

The same actions are available from the **Volumes** panel in the web UI.

Rules:

- A volume cannot be resized or deleted while a build is using it.
- Expansion requires a StorageClass with `allowVolumeExpansion: true`.
- The k3s `local-path` class does not support expansion; use an EBS-backed class
  in production.
- Quotas are checked on create and expand.

## Quotas

The portal enforces two application-level quotas:

- `--volume-quota` / `ZETA_VOLUME_QUOTA` (default `20Gi`): total capacity of all
  managed volumes.
- `--volume-max` / `ZETA_VOLUME_MAX` (default `10Gi`): maximum size of one
  volume.

These are not Kubernetes `ResourceQuota` objects; the portal checks them itself
so it can return a clear error.

## Reset the cache

!!! warning "Shared cache"
    Deleting the cache PVC removes `ccache`, `uv`, and Go caches for all builds
    that use it.

```bash
zeta volumes --role cache
kubectl --context local -n zeta-poc delete job \
  -l zeta.io/managed-by=portal --ignore-not-found
kubectl --context local -n zeta-poc delete pvc zeta-poc-cache
```

The PVC is recreated on the next build. Profiles with a dedicated `cachePVC`
keep their own volume.

## Reset artifacts

```bash
zeta volumes --role artifacts
kubectl --context local -n zeta-poc delete pvc zeta-poc-artifacts
```

The shared PVC is recreated on the next build. Profiles with a dedicated
`artifactsPVC` keep their own volume.

## Production storage

On k3s the default StorageClass is `local-path`, so volumes are node-local. In
production, set the portal `--storage-class` / `ZETA_STORAGE_CLASS` to an
EBS-backed StorageClass, or set `storageClass` per profile for dedicated
volumes. This gives the expected durability and attach/detach behavior.

## Caveats

- Artifacts are stored per language path, not per build. A newer build of the
  same profile can overwrite the previous artifact directory.
- There is no automatic volume garbage collection.
- Deleting a namespace deletes all its PVCs.
- `zeta volumes delete` is a convenience wrapper; you can also use `kubectl`.
