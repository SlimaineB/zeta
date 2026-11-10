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
| `--storage-class` | `ZETA_STORAGE_CLASS` | `local-path` | StorageClass for shared PVCs |
| `--volume-quota` | `ZETA_VOLUME_QUOTA` | `20Gi` | Total capacity of managed volumes |
| `--volume-max` | `ZETA_VOLUME_MAX` | `10Gi` | Maximum size of one managed volume |
| `--env-idle-timeout` | `ZETA_ENV_IDLE_TIMEOUT` | `15m` | Scale idle environments to zero (`0` disables) |
| `--oidc-issuer` | `ZETA_OIDC_ISSUER` | empty | OIDC issuer |
| `--oidc-client-id` | `ZETA_OIDC_CLIENT_ID` | `zeta-portal` | OIDC client ID |
| `--oidc-client-secret` | `ZETA_OIDC_CLIENT_SECRET` | empty | OIDC client secret |
| `--oidc-redirect-url` | `ZETA_OIDC_REDIRECT_URL` | auto | OIDC callback URL |
| `--oidc-scopes` | `ZETA_OIDC_SCOPES` | `openid,profile,email,groups` | OIDC scopes |
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
  - -oidc-client-id=zeta-portal
  - -oidc-client-secret=<secret>
  - -oidc-redirect-url=https://zeta.example.com/oidc/callback
  - -oidc-session-key=<32+ random characters>
  - -oidc-session-ttl=12h
  - -secure-cookie
```

The OIDC provider must allow exactly the callback URL. The portal uses the
`email`, `preferred_username`, and `groups` claims. Per-group RBAC is not
implemented yet: every authenticated user can launch and view builds in the
namespace.

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

| Manifest | Contents |
|---|---|
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

The portal Deployment in `platform/manifests/portal.yaml` sets the namespace,
storage class, quotas, idle timeout, and dev auth. Adjust the `args` list for
production and add the OIDC values from your secret store.
