# Helm chart reference

The chart lives in [`charts/zeta`](https://github.com/SlimaineB/zeta/tree/main/charts/zeta)
and is published as an OCI artifact:

```bash
helm show values oci://ghcr.io/slimaineb/charts/zeta --version 0.1.0
```

## Values

### Images

| Value | Default | Description |
|---|---|---|
| `image.registry` | `ghcr.io/slimaineb` in the published chart | Registry prefix for all Zeta images |
| `image.repository` | `zeta/portal` | Portal repository |
| `image.tag` | chart `appVersion` | Portal tag; also used for `syncd`, init, and Git server images |
| `image.pullPolicy` | `IfNotPresent` | Pull policy |
| `image.pullSecrets` | `[]` | Pull secrets for the portal and Git server |

The portal receives `-image-registry` and `-image-tag`, so profile images and
helper images (context init, `syncd`, Git server) are rewritten consistently.
For example, with `image.registry=ghcr.io/owner` and tag `0.1.0`:

```text
ghcr.io/owner/zeta/portal:0.1.0
ghcr.io/owner/zeta/cpp:0.1.0
ghcr.io/owner/zeta/python-uv:0.1.0
ghcr.io/owner/zeta/go:0.1.0
ghcr.io/owner/zeta/git-server:0.1.0
```

### Portal

| Value | Default | Description |
|---|---|---|
| `fullnameOverride` | `zeta-portal` | Fixed portal name; keep it for CLI and profile compatibility |
| `portal.replicaCount` | `1` | Portal replicas; use one unless you add distributed locking |
| `portal.addr` | `:8080` | Listen address |
| `portal.storageClass` | `local-path` | StorageClass for shared and environment PVCs |
| `portal.volumeQuota` | `20Gi` | Total managed volume capacity |
| `portal.volumeMax` | `10Gi` | Maximum size of one managed volume |
| `portal.envIdleTimeout` | `15m` | Scale idle environments to zero; `0` disables |
| `portal.resources` | small requests/limits | Portal CPU and memory |
| `portal.podSecurityContext` | non-root `65532` | Pod security context |
| `portal.securityContext` | read-only root, no caps | Container security context |
| `portal.extraArgs` | `[]` | Extra portal flags |
| `portal.extraEnv` | `[]` | Extra portal environment variables |
| `service.type` | `ClusterIP` | Service type |
| `service.port` | `80` | Service port |

### Ingress

| Value | Default | Description |
|---|---|---|
| `ingress.enabled` | `true` | Create an Ingress |
| `ingress.className` | `traefik` | Ingress class; use `nginx` on many cloud clusters |
| `ingress.host` | `zeta.local` | Host name |
| `ingress.annotations` | `{}` | Ingress annotations |
| `ingress.tls` | `[]` | Ingress TLS configuration |

### Authentication

| Value | Default | Description |
|---|---|---|
| `auth.dev` | `true` | Built-in development users |
| `auth.sessionKey` | generated | Session HMAC key; generated once and preserved |
| `auth.sessionTTL` | `12h` | Session lifetime |
| `auth.secureCookie` | `false` | Set `Secure` cookies; enable behind HTTPS |
| `auth.existingSecret` | empty | Existing Secret with `session-key` and `oidc-client-secret` |
| `auth.oidc.issuer` | empty | OIDC issuer; required when `auth.dev=false` |
| `auth.oidc.discoveryURL` | empty | Discovery URL; defaults to the issuer |
| `auth.oidc.authorizationURL` | empty | Authorization endpoint override |
| `auth.oidc.clientID` | `zeta-portal` | OIDC client ID |
| `auth.oidc.clientSecret` | empty | OIDC client secret |
| `auth.oidc.redirectURL` | auto | OIDC callback URL |
| `auth.oidc.scopes` | `openid,profile,email,groups` | OIDC scopes |
| `auth.oidc.allowedGroups` | empty | Comma-separated allowed groups |
| `auth.oidc.allowedUsers` | empty | Comma-separated allowed usernames, emails, or subjects |
| `auth.oidc.usernameClaim` | `preferred_username` | Username claim |
| `auth.oidc.emailClaim` | `email` | Email claim |
| `auth.oidc.groupsClaim` | `groups` | Groups claim |

For production, create the Secret yourself so the client secret is not in the
Helm release values:

```bash
kubectl -n zeta-poc create secret generic zeta-auth \
  --from-literal=session-key="$(openssl rand -hex 32)" \
  --from-literal=oidc-client-secret='<oidc-secret>'

helm upgrade --install zeta oci://ghcr.io/slimaineb/charts/zeta \
  --version 0.1.0 \
  --namespace zeta-poc \
  --set auth.dev=false \
  --set auth.existingSecret=zeta-auth \
  --set auth.oidc.issuer=https://idp.example.com/realms/dev \
  --set auth.oidc.clientID=zeta-portal \
  --set auth.oidc.redirectURL=https://zeta.example.com/oidc/callback \
  --set auth.secureCookie=true
```

The Secret must contain the key `session-key`. `oidc-client-secret` is optional
and is read by the portal through `ZETA_OIDC_CLIENT_SECRET`.

Set `auth.oidc.discoveryURL` when the portal must fetch discovery from an
internal address while `auth.oidc.issuer` remains the public issuer. Set
`auth.oidc.authorizationURL` when the browser must use a different authorization
endpoint than the one in the discovery document.

`auth.oidc.allowedGroups` and `auth.oidc.allowedUsers` are optional allowlists.
When set, a user must match at least one group and at least one user entry.
Entries in `allowedUsers` can be usernames, emails, or subjects. Claim names can
be changed for providers such as Okta or Microsoft Entra ID.

### Persistence

| Value | Default | Description |
|---|---|---|
| `persistence.artifacts.size` | `1Gi` | Shared artifacts PVC |
| `persistence.artifacts.storageClass` | `portal.storageClass` | Artifacts StorageClass |
| `persistence.cache.size` | `2Gi` | Shared cache PVC |
| `persistence.cache.storageClass` | `portal.storageClass` | Cache StorageClass |
| `persistence.contexts.size` | `2Gi` | Uploaded context PVC |
| `persistence.contexts.storageClass` | `portal.storageClass` | Context StorageClass |

PVC names are fixed to `zeta-poc-artifacts`, `zeta-poc-cache`, and
`zeta-poc-contexts` because the portal uses them directly. They carry
`helm.sh/resource-policy: keep`, so `helm uninstall` preserves the data.

### Profiles

| Value | Default | Description |
|---|---|---|
| `profiles.inline` | empty | Inline profiles YAML |
| `profiles.existingConfigMap` | empty | Existing ConfigMap with a `profiles.yaml` key |

Profile images use `image.registry` and `image.tag`, like the portal and helper
images. Use custom profile YAML when a profile needs a different image.

Inline example:

```bash
helm upgrade --install zeta ./charts/zeta \
  --namespace zeta-poc \
  --set image.registry=ghcr.io/slimaineb \
  --set image.tag=0.1.0 \
  --set-file profiles.inline=./profiles.yaml
```

Existing ConfigMap:

```bash
kubectl -n zeta-poc create configmap zeta-profiles \
  --from-file=profiles.yaml=./profiles.yaml

helm upgrade --install zeta oci://ghcr.io/slimaineb/charts/zeta \
  --version 0.1.0 \
  --namespace zeta-poc \
  --set profiles.existingConfigMap=zeta-profiles
```

See [Build profiles](../user-guide/profiles.md).

### Git server

| Value | Default | Description |
|---|---|---|
| `gitServer.enabled` | `false` | Deploy the example Git server |
| `gitServer.image.repository` | `zeta/git-server` | Git server repository |
| `gitServer.image.tag` | `image.tag` | Git server tag |
| `gitServer.service.name` | `git-server` | Service name used by profiles |
| `gitServer.service.port` | `9418` | Git protocol port |
| `gitServer.persistence.claimName` | `zeta-poc-git` | Data PVC |
| `gitServer.persistence.size` | `1Gi` | Data PVC size |
| `gitServer.persistence.storageClass` | `portal.storageClass` | Data PVC StorageClass |
| `gitServer.resources` | `{}` | Git server resources |
| `gitServer.podSecurityContext` | non-root `1000` | Pod security context |

The Service name must remain `git-server` for the built-in profiles, which use
`git://git-server/<repo>.git`. Enable it only if you want the example
repositories; real projects can use any reachable Git URL.

### Scheduling

| Value | Default | Description |
|---|---|---|
| `nodeSelector` | `{}` | Portal node selector |
| `tolerations` | `[]` | Portal tolerations |
| `affinity` | `{}` | Portal affinity |

## Examples

### Local chart against published images

```bash
helm upgrade --install zeta ./charts/zeta \
  --namespace zeta-poc --create-namespace \
  --set image.registry=ghcr.io/slimaineb \
  --set image.tag=0.1.0
```

### OIDC with an existing Secret

```bash
helm upgrade --install zeta oci://ghcr.io/slimaineb/charts/zeta \
  --version 0.1.0 \
  --namespace zeta-poc \
  --set auth.dev=false \
  --set auth.existingSecret=zeta-auth \
  --set auth.oidc.issuer=https://idp.example.com/realms/dev \
  --set auth.oidc.clientID=zeta-portal \
  --set auth.oidc.redirectURL=https://zeta.example.com/oidc/callback
```

### Git server and custom storage

```bash
helm upgrade --install zeta oci://ghcr.io/slimaineb/charts/zeta \
  --version 0.1.0 \
  --namespace zeta-poc \
  --set portal.storageClass=gp3 \
  --set ingress.className=nginx \
  --set ingress.host=zeta.example.com \
  --set gitServer.enabled=true
```

## Names to keep stable

| Resource | Name | Why |
|---|---|---|
| Portal Deployment, Service, Ingress | `zeta-portal` | CLI and sync URLs |
| Auth Secret | `zeta-portal-auth` | Session and OIDC secret |
| Shared PVCs | `zeta-poc-*` | Portal constants |
| Git server Service | `git-server` | Default profile repositories |
| Git server PVC | `zeta-poc-git` | Example repository data |

If you change `fullnameOverride`, update the CLI server URL and any custom
profiles that reference `ws://zeta-portal`.

## Release pipeline

The GitHub Actions workflow [`.github/workflows/release.yml`](https://github.com/SlimaineB/zeta/blob/main/.github/workflows/release.yml)
runs on `v*` tags:

1. builds and pushes `portal`, `cpp`, `go`, `python-uv`, and `git-server` images
   to `ghcr.io/<owner>/zeta/<image>:<version>` and `:latest`;
2. builds the CLI for Linux, macOS, and Windows on `amd64` and `arm64`;
3. packages the chart with the release version as both chart version and
   `appVersion`, patches `image.registry`, and pushes it to
   `oci://ghcr.io/<owner>/charts/zeta`;
4. creates a GitHub Release with the binaries, chart, and `checksums.txt`.

A `v0.1.0` tag therefore produces chart version `0.1.0`, image tag `0.1.0`, and
CLI assets such as `zeta-linux-amd64` and `zeta-windows-amd64.exe`.
