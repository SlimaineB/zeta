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
- **OIDC**: authorization code flow with PKCE, nonce verification, the configured
  issuer, client ID, secret, scopes, and callback URL.

The short-lived login state is stored in a signed `zeta_oidc` cookie, not in
process memory, so login works with multiple portal replicas. Sessions are signed
with an HMAC key (`--oidc-session-key`) and stored in the `zeta_session` cookie.
If no key is provided, a random key is generated at startup; all sessions are
invalidated on restart.

OIDC claims are configurable with `--oidc-username-claim`,
`--oidc-email-claim`, and `--oidc-groups-claim`. Groups and profile data are
merged from the userinfo endpoint when missing from the ID token.
`--oidc-allowed-groups` and `--oidc-allowed-users` optionally restrict who can
sign in; when both are set, a user must match both allowlists.

In production:

- set a fixed, random session key of at least 32 characters;
- set `--secure-cookie` behind HTTPS;
- keep the session TTL short enough for your policy (default `12h`);
- configure the OIDC provider to allow exactly the callback URL;
- use an allowlist when the provider can authenticate users outside your team.

## Transport and browser protections

- Browser WebSocket endpoints (`/api/envs/{id}/exec` and
  `/api/sync/{id}/client`) require an `Origin` header matching the portal host.
- The sync agent authenticates with `Authorization: Bearer <token>`. The older
  `?token=` query parameter is still accepted for backward compatibility but is
  no longer used by newly created environments or build Jobs.
- State-changing API requests reject cross-site `Sec-Fetch-Site`, `Origin`, or
  `Referer` values. Non-browser clients that send none of these headers, such as
  the CLI, are still accepted.
- The session cookie is signed but not encrypted. It contains identity and group
  data, not credentials. Logout clears the local cookie; the portal does not
  implement IdP-initiated or RP-initiated revocation yet.

## Authorization

The portal's ServiceAccount is namespace-scoped. It can create and inspect Jobs,
Pods, Pod logs, Deployments, Secrets, and PVCs in `zeta-poc`.

Environments and builds are owner-scoped. The portal records the authenticated
user's email (or username when the provider omits email) as the owner when
creating an environment or build. Lifecycle
operations, exec, sync, logs, metrics, and artifact downloads require the owner
or membership in the `admin` / `admins` group. Listing builds and environments
remains available to every authenticated user. Resources created before owner
tracking, or with an empty owner, remain accessible to any authenticated user for
backward compatibility.

There is still no full per-group RBAC, and volumes remain shared across the
namespace. The `groups` claim is collected and can be used in
`--oidc-allowed-groups`, but it is not used for arbitrary group-to-resource
policy.

## Pod security

Build Jobs and environment pods use:

- `runAsNonRoot: true`, `runAsUser: 1000`, `runAsGroup: 1000`;
- `automountServiceAccountToken: false`;
- `seccompProfile: RuntimeDefault`;
- all Linux capabilities dropped;
- no privileged mode, no host namespaces, no host paths.

The portal itself runs as non-root `65532:65532` with a read-only root
filesystem.

## Browser IDE

The `code-server` sidecar is part of the environment pod and shares
`/workspace`. It binds to `127.0.0.1:8080`; no Service or Ingress exposes it.
The browser reaches it only through `/api/envs/<id>/ide/` on the portal:

- the portal requires a valid session and checks environment ownership;
- the portal opens a Kubernetes port-forward (`pods/portforward`) to the pod;
- HTTP and WebSocket traffic is reverse-proxied, including the VS Code
  extension-host socket;
- code-server authentication is disabled because the portal is the
  authentication boundary;
- the port-forward is closed when the environment is deactivated, deleted, or
  reaped;
- an open IDE WebSocket counts as activity and prevents idle scale-down.

The portal service account therefore needs `get` and `create` on
`pods/portforward` in the environment namespace.

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

- No full per-group RBAC or strong multi-tenant isolation; volumes remain shared.
- No IdP logout/revocation; sessions remain valid until expiry after local logout.
- The session cookie is signed but not encrypted.
- No private SSH repository support; use HTTPS tokens.
- Artifacts are stored per language path, not per build.
- No S3/MinIO backend; PVCs use `local-path` on the reference single-node cluster.
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
