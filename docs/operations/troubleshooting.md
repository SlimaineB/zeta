# Troubleshooting

## Quick diagnostics

```bash
make doctor
kubectl --context local get nodes
kubectl --context local -n zeta-poc get deploy,svc,ingress,pods,pvc,jobs
kubectl --context local -n zeta-poc logs deploy/zeta-portal --tail=100
```

## Portal and cluster

### `The connection to the server 127.0.0.1:6443 was refused`

k3s is not running. Restart it:

```bash
sudo -n bash -c 'nohup k3s server >/tmp/k3s-server.log 2>&1 &'
kubectl --context local get nodes
```

### `zeta: session missing; run zeta login`

The CLI has no local session. Run:

```bash
zeta login --server http://zeta.local --email dev@example.com --password dev
```

### `not authenticated` or `session expired`

The cookie is invalid or expired. Run `zeta login` again. If the portal restarted
without a fixed `--oidc-session-key`, all sessions were invalidated.

### OIDC sign-in fails or redirects back to login

Check that:

- the provider callback URL is exactly `https://<host>/oidc/callback`;
- `--oidc-issuer` matches the `issuer` returned by discovery;
- `--oidc-discovery-url` is reachable from the portal if you use a split-horizon
  discovery endpoint;
- the user matches `--oidc-allowed-groups` and `--oidc-allowed-users` when those
  allowlists are configured;
- all replicas use the same `--oidc-session-key`; the short-lived `zeta_oidc`
  flow cookie is signed with it.

If the provider returns groups only through userinfo, make sure the `groups`
scope is requested or set `--oidc-groups-claim` to the claim your provider uses.

### Ingress 404

The `Host` header does not match `zeta.local`. Check the hosts entry or use a
port-forward:

```bash
kubectl --context local -n zeta-poc port-forward \
  --address 0.0.0.0 svc/zeta-portal 8080:80
```

### Port-forward not reachable from Windows

Use `--address 0.0.0.0`:

```bash
kubectl --context local -n zeta-poc port-forward \
  --address 0.0.0.0 svc/zeta-portal 8080:80
```

## Builds

### `invalid repository URL`

Use `git://`, `http://`, `https://`, or `ssh://`. `file://` URLs and local paths
are rejected.

### `invalid Git reference`

The commit contains forbidden characters (`..`, spaces, `;`, `$`, etc.). Use a
SHA, tag, or simple branch name.

### `remote HEAD refers to nonexistent ref`

The bare repository points at a missing branch. Fix it:

```bash
kubectl --context local -n zeta-poc exec deploy/git-server -- \
  sh -c 'git --git-dir=/srv/git/cpp.git symbolic-ref HEAD refs/heads/main'
```

### `dubious ownership` in a Job

The Job configures `safe.directory /workspace`. If a custom profile clones
elsewhere, add the same Git configuration.

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
kubectl --context local -n zeta-poc describe pod <pod>
```

The reference k3s setup uses `local-path`.

### `PodInitializing` in logs

Context extraction (tar or Git clone) is still running. Wait or inspect the init
container logs:

```bash
kubectl --context local -n zeta-poc logs <pod> -c context
```

### Context too large (`413`)

The upload is limited to 512 MiB. Exclude large directories:

```bash
zeta build --exclude node_modules --exclude data
```

### Builds disappear after one hour

This is the Job TTL (`TTLSecondsAfterFinished: 3600`). Change it in
`portal/internal/kube/repository.go` and redeploy for longer retention.

### Artifact download fails with `unexpected EOF`

Artifacts are streamed as gzip+base64 through pod logs to avoid binary log
corruption. Check the portal logs and the Job status:

```bash
kubectl --context local -n zeta-poc logs deploy/zeta-portal --tail=100
zeta status <id>
```

## Environments

### `activate: environment <id> not found`

`zeta activate` only starts an existing environment. Create it first with
`zeta init`, then run `zeta activate` again.

### `zeta activate` hangs

Check the pod and the sync sidecar:

```bash
kubectl --context local -n zeta-poc get pods
kubectl --context local -n zeta-poc describe pod -l zeta.io/env=<id>
kubectl --context local -n zeta-poc get pvc
kubectl --context local -n zeta-poc logs deploy/zeta-env-<id> -c syncd
kubectl --context local -n zeta-poc logs deploy/zeta-env-<id> -c env
```

Common causes: missing image, PVC not bound, or the portal cannot reach the
Deployment.

The portal now waits up to 90 seconds for the environment pod and reports an
actionable reason instead of a generic "not running":

- `cannot mount its volume (FailedMount): ...` — the workspace or cache PVC
  cannot be attached. Check the StorageClass, the PVC events and the node's
  disk space.
- `is unschedulable: ...` — no node satisfies the profile resource requests.
- `image ... cannot be pulled (ImagePullBackOff): ...` — wrong image name or
  missing pull secret.
- `cannot start (CreateContainerConfigError): ...` — invalid env/secret
  reference in the profile.

If the volume is stuck, recreate the environment after fixing the StorageClass
or free space; the workspace PVC is preserved on scale-down but a broken mount
requires a new pod.

### `429` / `concurrent build limit` or `active environment limit`

Zeta enforces fair-use quotas. A `429` means you reached
`--max-builds-per-user` or `--max-envs-per-user`. Wait for a build to finish, or
run `zeta deactivate --name <env>` before activating another environment. See
[configuration](../reference/configuration.md#fair-use-and-parallel-tasks).

### Shell exits immediately

The profile image must contain a shell (`sh` or `bash`). Check the `env`
container logs and the image entrypoint.

### Files do not come back locally

- `build/` and `dist/` are synced back, but `.git`, `.venv`, caches, and
  `node_modules` are excluded.
- Check `exclude` in `zeta.yaml` and `--exclude`.
- Confirm the sync session is still connected.

### `zeta code` says the IDE is not ready

The first `zeta code` call adds the `ide` sidecar to an existing environment and
triggers a rollout. Wait for the new pod, then retry:

```bash
kubectl --context local -n zeta-poc get pods -l zeta.io/env=<id>
kubectl --context local -n zeta-poc logs deploy/zeta-env-<id> -c ide
```

If the pod stays `2/3`, check that `codercom/code-server:4.143.1` is present on
the node (or pullable) and that the portal Role includes `pods/portforward`.
The sidecar binds to `127.0.0.1:8080`, so a normal HTTP probe from kubelet cannot
reach it; the portal waits for the container and opens a port-forward instead.

### Share link is rejected

A share link is invalid after its TTL, after `zeta share revoke`, after the
environment is deactivated or deleted, or after a portal restart. Create a new
link with `zeta share`. A guest that receives `401 not authenticated` should
open the `/s/<token>` link again to refresh the guest cookie.

### Environment stops by itself

This is the idle reaper. Default `--env-idle-timeout` is `15m`; `0` disables it.
Run `zeta activate` again to resume with the same volume.

### `rm` returns `504`

The portal restarted during the request or the PVC is still terminating. Retry:

```bash
zeta rm <id>
```

If the PVC is stuck:

```bash
kubectl --context local -n zeta-poc get pvc zeta-env-<id>
kubectl --context local -n zeta-poc describe pvc zeta-env-<id>
```

### Namespace stuck in `Terminating`

Check for remaining namespaced resources and finalizers:

```bash
kubectl --context local get ns <name> -o yaml
kubectl --context local -n <name> get all,pvc,secret,sa,role,rolebinding
```

Delete remaining workloads and PVCs, then wait for the namespace controller. As
a last resort, clear the namespace finalizer through the `finalize` subresource.

## Volumes

### Resize fails

- The volume is in use by a build.
- The StorageClass does not support expansion. `local-path` does not; use an
  EBS-backed class with `allowVolumeExpansion: true`.

### Quota exceeded

Check `zeta volumes` and raise `--volume-quota` / `--volume-max`, or delete an
unused volume.

## Logs and debugging

```bash
# Portal
kubectl --context local -n zeta-poc logs deploy/zeta-portal -f

# Build pod
kubectl --context local -n zeta-poc get pods
kubectl --context local -n zeta-poc describe pod <pod>
kubectl --context local -n zeta-poc logs <pod> -c context
kubectl --context local -n zeta-poc logs <pod> -c build

# Environment
kubectl --context local -n zeta-poc logs deploy/zeta-env-<id> -c env
kubectl --context local -n zeta-poc logs deploy/zeta-env-<id> -c syncd

# Events
kubectl --context local -n zeta-poc get events --sort-by=.lastTimestamp
```
