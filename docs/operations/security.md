# Security

This page describes Zeta's security model, its guarantees, and its current
limitations.

## Security model

- Developers never receive a kubeconfig.
- The portal uses a namespace-scoped ServiceAccount.
- Jobs and environments run as non-root `1000:1000` with no service account
  token.
- Pod Security Admission `restricted`, seccomp `RuntimeDefault`, all
  capabilities dropped.
- The portal root filesystem is read-only.
- Build context archives are validated against path traversal, out-of-context
  links, and special files.
- Sync paths are validated and dangerous symlinks are rejected.

## Authentication and sessions

The portal supports two modes:

- **Development mode** (`--oidc-dev`): built-in users and passwords, intended
  for local use only.
- **OIDC**: authorization code flow with the configured issuer, client ID,
  secret, scopes, and callback URL.

Sessions are signed with an HMAC key (`--oidc-session-key`) and stored in the
`zeta_session` cookie. If no key is provided, a random key is generated at
startup; all sessions are invalidated on restart.

In production:

- set a fixed, random session key of at least 32 characters;
- set `--secure-cookie` behind HTTPS;
- keep the session TTL short enough for your policy (default `12h`);
- configure the OIDC provider to allow exactly the callback URL.

## Authorization

The portal's ServiceAccount is namespace-scoped. It can create and inspect Jobs,
Pods, Pod logs, Deployments, Secrets, and PVCs in `zeta-poc`.

Current limitation: **there is no per-group RBAC**. Every authenticated user can
launch and view builds and environments in the namespace. The `groups` claim is
collected but not enforced.

## Pod security

Build Jobs and environment pods use:

- `runAsNonRoot: true`, `runAsUser: 1000`, `runAsGroup: 1000`;
- `automountServiceAccountToken: false`;
- `seccompProfile: RuntimeDefault`;
- all Linux capabilities dropped;
- no privileged mode, no host namespaces, no host paths.

The portal itself runs as non-root `65532:65532` with a read-only root
filesystem.

## Context validation

The portal validates uploaded archives:

- no absolute paths;
- no `..` traversal;
- no links escaping the context;
- no special files (devices, sockets, etc.);
- upload limited to 512 MiB (2 GiB uncompressed).

Git mode validates the repository URL scheme and rejects shell metacharacters
and `..` in references.

## Sync validation

The sync client and server validate paths and reject dangerous symlinks. Sync is
scoped to the workspace and the configured exclusions.

## Secrets

- Build secrets are injected with `envFromSecrets` and marked `optional: true`.
- The example `zeta-build-secrets` Secret holds `GIT_TOKEN`, `GIT_USERNAME`, and
  Go proxy settings.
- Environment sync tokens are stored in `zeta-env-<id>-sync` and mounted only
  into the `syncd` container.
- The portal never logs secret values.

Use Kubernetes Secrets, not literals in profiles, for credentials.

## Limits and known gaps

- No per-group RBAC or strong multi-tenant isolation.
- No private SSH repository support; use HTTPS tokens.
- Artifacts are stored per language path, not per build.
- No S3/MinIO backend; PVCs use `local-path` on the POC cluster.
- No automatic volume garbage collection.
- Volume expansion requires a StorageClass with `allowVolumeExpansion: true`.
- Jobs and artifacts expire after one hour by default.
- No `.dockerignore`; use `--exclude` and default context exclusions.

## Hardening checklist

- [ ] Replace dev auth with OIDC.
- [ ] Set a fixed `--oidc-session-key`.
- [ ] Enable `--secure-cookie` and serve the portal over HTTPS.
- [ ] Use an EBS-backed StorageClass with encryption.
- [ ] Restrict network access to the ingress.
- [ ] Review the portal RBAC and namespace scope.
- [ ] Create build secrets with least privilege.
- [ ] Monitor portal and Job logs.
- [ ] Keep images pinned and rebuilt on security updates.
- [ ] Back up persistent environment workspaces.

## Reporting a vulnerability

Do not open a public issue for a security problem. Contact the repository owner
privately and include a description, reproduction steps, and impact.
