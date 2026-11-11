# Portal configuration

The portal is configured with flags or environment variables. Flags take
precedence.

## Flags and environment variables

| Flag | Environment variable | Default | Description |
|---|---|---|---|
| `--addr` | `ZETA_ADDR` | `:8080` | Listen address |
| `--namespace` | `ZETA_NAMESPACE` | `zeta-poc` | Build namespace |
| `--kubeconfig` | `ZETA_KUBECONFIG` | empty | Local kubeconfig (development) |
| `--master` | `ZETA_MASTER` | empty | Kubernetes API URL |
| `--profiles-file` | `ZETA_PROFILES_FILE` | empty | Additional profiles YAML |
| `--image-registry` | `ZETA_IMAGE_REGISTRY` | empty | Registry prefix for profile and helper images |
| `--image-tag` | `ZETA_IMAGE_TAG` | empty | Tag override for profile and helper images |
| `--ide-image` | `ZETA_IDE_IMAGE` | `codercom/code-server:4.143.1` | Browser IDE sidecar image; empty disables the IDE |
| `--storage-class` | `ZETA_STORAGE_CLASS` | `local-path` | StorageClass for shared PVCs |
| `--volume-quota` | `ZETA_VOLUME_QUOTA` | `20Gi` | Total capacity of managed volumes |
| `--volume-max` | `ZETA_VOLUME_MAX` | `10Gi` | Maximum size of one managed volume |
| `--env-idle-timeout` | `ZETA_ENV_IDLE_TIMEOUT` | `15m` | Scale idle environments to zero (`0` disables) |
| `--oidc-issuer` | `ZETA_OIDC_ISSUER` | empty | OIDC issuer |
| `--oidc-discovery-url` | `ZETA_OIDC_DISCOVERY_URL` | issuer | OIDC discovery URL |
| `--oidc-authorization-url` | `ZETA_OIDC_AUTHORIZATION_URL` | discovery | OIDC authorization endpoint override |
| `--oidc-client-id` | `ZETA_OIDC_CLIENT_ID` | `zeta-portal` | OIDC client ID |
| `--oidc-client-secret` | `ZETA_OIDC_CLIENT_SECRET` | empty | OIDC client secret |
| `--oidc-redirect-url` | `ZETA_OIDC_REDIRECT_URL` | auto | OIDC callback URL |
| `--oidc-scopes` | `ZETA_OIDC_SCOPES` | `openid,profile,email,groups` | OIDC scopes |
| `--oidc-allowed-groups` | `ZETA_OIDC_ALLOWED_GROUPS` | empty | Comma-separated allowed groups |
| `--oidc-allowed-users` | `ZETA_OIDC_ALLOWED_USERS` | empty | Comma-separated allowed usernames, emails, or subjects |
| `--oidc-username-claim` | `ZETA_OIDC_USERNAME_CLAIM` | `preferred_username` | Username claim |
| `--oidc-email-claim` | `ZETA_OIDC_EMAIL_CLAIM` | `email` | Email claim |
| `--oidc-groups-claim` | `ZETA_OIDC_GROUPS_CLAIM` | `groups` | Groups claim |
| `--oidc-session-key` | `ZETA_OIDC_SESSION_KEY` | random | Session HMAC key |
| `--oidc-session-ttl` | `ZETA_OIDC_SESSION_TTL` | `12h` | Session lifetime |
| `--oidc-dev` | `ZETA_OIDC_DEV` | `false` | Local development auth |
| `--secure-cookie` | `ZETA_SECURE_COOKIE` | `false` | `Secure` cookies |

## Rules

- Either `--oidc-issuer` or `--oidc-dev` is required.
- `--namespace` is required.
- If `--oidc-session-key` is empty, a random key is generated at startup. All
  sessions are invalidated on restart. Set a fixed key in production.

## OIDC in production

Example Deployment arguments:

```yaml
args:
  - -addr=:8080
  - -namespace=zeta-poc
  - -oidc-issuer=https://idp.example.com/realms/dev
  - -oidc-discovery-url=https://idp.example.com/realms/dev
  - -oidc-client-id=zeta-portal
  - -oidc-client-secret=<secret>
  - -oidc-redirect-url=https://zeta.example.com/oidc/callback
  - -oidc-allowed-groups=zeta-users
  - -oidc-username-claim=preferred_username
  - -oidc-email-claim=email
  - -oidc-groups-claim=groups
  - -oidc-session-key=<32+ random characters>
  - -oidc-session-ttl=12h
  - -secure-cookie
```

The OIDC provider must allow exactly the callback URL. The portal uses the
configured `--oidc-username-claim`, `--oidc-email-claim`, and
`--oidc-groups-claim`; defaults are `preferred_username`, `email`, and `groups`.
Groups and profile data are also read from the userinfo endpoint when the ID
token does not contain them.

`--oidc-allowed-groups` and `--oidc-allowed-users` are optional allowlists. When
set, the user must match at least one allowed group and at least one allowed
user entry. A user entry can be a username, email, or subject.

Use `--oidc-discovery-url` when the portal must reach discovery through an
internal address while the browser uses a public issuer. Use
`--oidc-authorization-url` when the discovery document advertises an internal
authorization endpoint that the browser cannot reach.

The login flow uses PKCE, verifies the ID token nonce, and stores its short-lived
state in a signed `zeta_oidc` cookie, so it works across multiple portal
replicas. Sessions are stored in the signed `zeta_session` cookie. Environments
and builds are owner-scoped: lifecycle, exec, sync, logs, metrics, and artifact
downloads require the owner or an `admin` / `admins` group member. Listing
builds and environments remains available to every authenticated user. Full
per-group RBAC is still not implemented.

## Development authentication

With `--oidc-dev`, the portal accepts the built-in users:

| Email | Password | Groups |
|---|---|---|
| `dev@example.com` | `dev` | `devs` |
| `admin@example.com` | `admin` | `admins` |
| `alice@example.com` | `alice` | `backend` |

## Environment idle timeout

`--env-idle-timeout` controls the persistent environment reaper:

- default `15m`;
- `0` disables automatic scale-down;
- the portal checks at most once per minute;
- an environment with an attached shell or sync client is never reaped;
- scaling down preserves the workspace PVC.

## Image overrides

`--image-registry` and `--image-tag` rewrite the images used by builds:

- profile images such as `zeta/cpp:0.1` become
  `<registry>/zeta/cpp:<tag>`;
- the context init image (`zeta/git-server`), the sync sidecar
  (`zeta/portal`/`syncd`), and the optional Git server use the same registry and
  tag;
- images that already contain a registry (for example `ghcr.io/...` or
  `localhost/...`) are left unchanged;
- the artifact helper image remains `busybox:1.36` from Docker Hub.

Example:

```bash
zeta-portal \
  -image-registry=ghcr.io/slimaineb \
  -image-tag=0.1.0
```

The Helm chart sets these values from `image.registry` and `image.tag`. Use the
same tag as the portal image so the `syncd` sidecar matches the portal. See
[Helm chart reference](../platform/helm-chart.md#images).

## Profiles file

`--profiles-file` points to an additional YAML file. It is merged with
`portal/internal/builds/defaults.yaml` by profile name. See
[Profiles](../user-guide/profiles.md).

## Storage

- `--storage-class` is used for shared and environment PVCs unless a profile sets
  its own `storageClass`.
- Storage class is a platform concern and is not configurable from `zeta.yaml`.
- `--volume-quota` and `--volume-max` are application-level quotas checked on
  create and expand.

See [Volumes and artifacts](../user-guide/volumes.md).

## Kubernetes resources

The Helm chart in `charts/zeta` is the recommended installation path. The raw
manifests remain for local development and for understanding the resources.

| Manifest | Contents |
|---|---|
| `charts/zeta/` | Helm chart for the portal, RBAC, PVCs, Ingress, and optional Git server |
| `platform/manifests/namespace.yaml` | Namespace and Pod Security labels |
| `platform/manifests/pvc.yaml` | Shared artifacts PVC |
| `platform/manifests/cache-pvc.yaml` | Shared build cache PVC |
| `platform/manifests/context-pvc.yaml` | Shared local context PVC |
| `platform/manifests/portal.yaml` | ServiceAccount, RBAC, Deployment, Service, Ingress |
| `platform/manifests/git-server.yaml` | Optional example Git server |

## Naming and labels

| Resource | Name |
|---|---|
| Namespace | `zeta-poc` |
| Portal Deployment | `zeta-portal` |
| Portal Service | `zeta-portal` |
| Portal Ingress | `zeta-portal` |
| Build Jobs | `zeta-<profile>-<id>` |
| Environment Deployment | `zeta-env-<id>` |
| Environment PVC | `zeta-env-<id>` |
| Environment sync Secret | `zeta-env-<id>-sync` |

Managed resources carry `zeta.io/managed-by=portal` and role/profile labels.

## Deployment environment

With Helm, configure the portal through values such as `portal.storageClass`,
`portal.volumeQuota`, `portal.envIdleTimeout`, `auth.*`, and `image.*`; see the
[Helm chart reference](../platform/helm-chart.md).

The raw `platform/manifests/portal.yaml` sets the same options in its `args`
list. Adjust it for production and add OIDC values from your secret store.
