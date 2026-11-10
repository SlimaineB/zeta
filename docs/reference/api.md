# HTTP API

All `/api/*` routes require a session cookie named `zeta_session`, except the
sync agent endpoint which uses a per-environment or per-build bearer token.

Base URL: `http://zeta.local` (or your ingress/port-forward address).

## Authentication

### Development login

```http
POST /api/login
Content-Type: application/json

{"username":"dev@example.com","password":"dev"}
```

Response:

```json
{"email":"dev@example.com","groups":["devs"]}
```

The response sets the `zeta_session` cookie.

### OIDC

| Route | Purpose |
|---|---|
| `GET /login` | Login page / redirect |
| `GET /oidc/login?next=/path` | Start the OIDC flow and return to `/path` |
| `GET /oidc/callback` | OIDC callback |

### Session

| Route | Purpose |
|---|---|
| `GET /api/me` | Current user |
| `POST /api/logout` | Close the session |

### Sync agent token

`GET /api/sync/{id}/agent` uses `Authorization: Bearer <token>` instead of the
cookie. The token is stored in the Secret `zeta-env-<id>-sync` (environments) or
in the build Job (build sync). `?token=<token>` is still accepted for
backward compatibility, but new environments and build Jobs use the header.

## Errors

Errors use a JSON body:

```json
{"error":"profile not found"}
```

| Status | Meaning |
|---|---|
| `400` | Invalid input, profile, repo, or reference |
| `401` | Missing or expired session |
| `403` | Cross-origin request or resource owned by another user |
| `404` | Build, environment, or volume not found |
| `409` | Volume in use or conflicting state |
| `413` | Context too large |
| `502` | Kubernetes API error |

## Endpoints

| Method | Route | Role |
|---|---|---|
| `POST` | `/api/login` | Development login |
| `GET` | `/oidc/login` | Start OIDC flow |
| `GET` | `/oidc/callback` | OIDC callback |
| `POST` | `/api/logout` | Close session |
| `GET` | `/api/me` | Current user |
| `GET` | `/api/profiles` | Available profiles |
| `GET` | `/api/builds` | List builds |
| `POST` | `/api/builds` | Submit a Git build (JSON) or local-context build (multipart) |
| `GET` | `/api/builds/{id}/logs` | Streaming logs |
| `GET` | `/api/builds/{id}/metrics` | Cache metrics |
| `GET` | `/api/builds/{id}/artifact` | Artifacts as `.tar` |
| `GET` | `/api/sync/{id}/client` | CLI-side sync WebSocket |
| `GET` | `/api/sync/{id}/agent` | Sidecar sync WebSocket (token) |
| `GET` | `/api/volumes` | Managed PVCs, quota, capacity, last use |
| `POST` | `/api/volumes/{name}/resize` | Expand a managed volume |
| `DELETE` | `/api/volumes/{name}` | Delete an idle managed volume |
| `POST` | `/api/envs` | Create an environment |
| `GET` | `/api/envs` | List environments |
| `POST` | `/api/envs/{id}/activate` | Start an environment |
| `POST` | `/api/envs/{id}/deactivate` | Stop an environment |
| `DELETE` | `/api/envs/{id}` | Delete an environment and its volume |
| `GET` | `/api/envs/{id}/exec` | Interactive exec WebSocket |
| `GET` | `/healthz` | Health check |

Build logs, metrics, and artifacts require the build owner or an `admin` /
`admins` group member. Environment lifecycle, exec, and sync endpoints require
the environment owner or an admin. Listing builds and environments remains
available to all authenticated users.

## Builds

### Submit a Git build

```http
POST /api/builds
Content-Type: application/json

{"profile":"go","repo":"git://git-server/go.git","commit":"main"}
```

Response `202 Accepted`:

```json
{"id":"zeta-go-8415dfd8487a"}
```

### Submit a local-context build

```http
POST /api/builds
Content-Type: multipart/form-data; boundary=...

profile=go
sync=true
context=@context.tar.gz
```

Fields: `profile`, `repo`, `commit`, `sync`, and file `context` (`tar.gz`).

Constraints:

- `profile` must exist.
- In Git mode, `repo` must be a `git://`, `http://`, `https://`, or `ssh://` URL.
- `commit` may be empty, `HEAD`, a branch, a tag, or a SHA.
- Upload limited to 512 MiB (2 GiB uncompressed).
- Absolute paths, `..`, out-of-context links, and special files are rejected.
- Shell metacharacters and `..` are rejected in Git references.

### List builds

```http
GET /api/builds
```

Returns an array of builds:

```json
[
  {
    "id": "zeta-go-8415dfd8487a",
    "profile": "go",
    "language": "go",
    "repo": "local:/home/user/project",
    "commit": "",
    "owner": "dev@example.com",
    "status": "succeeded",
    "pod": "zeta-go-8415dfd8487a-abcde",
    "createdAt": "2026-11-10T18:00:00Z",
    "startedAt": "2026-11-10T18:00:02Z",
    "finishedAt": "2026-11-10T18:00:15Z",
    "durationMs": 13000,
    "initDurationMs": 1000,
    "buildDurationMs": 12000,
    "metrics": {"hits": 12, "misses": 3, "hitRate": 80, "sizeBytes": 88080384}
  }
]
```

### Logs, metrics, artifacts

```http
GET /api/builds/{id}/logs
GET /api/builds/{id}/metrics
GET /api/builds/{id}/artifact
```

- `logs` streams `text/plain`.
- `metrics` returns the `CacheStats` JSON.
- `artifact` streams a `.tar` of the profile's `artifactPath`.

## Volumes

### List

```http
GET /api/volumes
```

```json
{
  "volumes": [
    {
      "name": "zeta-poc-cache",
      "role": "cache",
      "capacityBytes": 2147483648,
      "storageClass": "local-path",
      "createdAt": "2026-11-08T19:00:00Z",
      "lastUsedAt": "2026-11-10T18:00:00Z",
      "lastBuild": "zeta-go-8415dfd8487a",
      "lastProfile": "go",
      "lastOwner": "dev@example.com",
      "resizable": false,
      "inUse": false
    }
  ],
  "quota": {
    "maxTotalBytes": 21474836480,
    "usedBytes": 5368709120,
    "remainingBytes": 16106127360,
    "maxVolumeBytes": 10737418240
  }
}
```

### Resize

```http
POST /api/volumes/{name}/resize
Content-Type: application/json

{"size":"5Gi"}
```

### Delete

```http
DELETE /api/volumes/{name}
```

Returns `204 No Content`. Fails if the volume is in use.

## Environments

### Create

```http
POST /api/envs
Content-Type: application/json

{
  "name": "dev",
  "profile": "go",
  "image": "zeta/go:0.1",
  "size": "5Gi",
  "env": {"GOFLAGS": "-mod=mod"}
}
```

Response `201 Created`:

```json
{
  "id": "dev",
  "profile": "go",
  "active": false,
  "sizeBytes": 5368709120,
  "storageClass": "local-path",
  "createdAt": "2026-11-10T18:00:00Z"
}
```

`image` and `env` are optional. `name` is normalized to a slug. The
StorageClass is chosen by the platform (`--storage-class` /
`ZETA_STORAGE_CLASS`); the response reports the actual class.

### List

```http
GET /api/envs
```

Returns an array of `Environment`.

### Activate

```http
POST /api/envs/{id}/activate
```

```json
{"id":"dev","active":true}
```

If the environment does not exist, the portal returns `404` with
`environment <id> not found; run 'zeta init' first`.

### Deactivate

```http
POST /api/envs/{id}/deactivate
```

```json
{"id":"dev","active":false}
```

### Delete

```http
DELETE /api/envs/{id}
```

Returns `204 No Content`. Deletes the Deployment, pods, sync Secret, and PVC.

### Exec

`GET /api/envs/{id}/exec` upgrades to a WebSocket. Frames:

| First byte | Payload |
|---|---|
| `i` | Raw stdin bytes |
| `r` | JSON resize `{"cols":120,"rows":30}` |
| `o` | Raw stdout/stderr bytes (server to client) |

The exec runs in the `env` container, in `/workspace`, with a TTY.

## Sync WebSockets

| Route | Auth | Direction |
|---|---|---|
| `GET /api/sync/{id}/client` | Session cookie + same-origin `Origin` | CLI |
| `GET /api/sync/{id}/agent` | `Authorization: Bearer` | Pod sidecar |

The portal relays messages between the CLI and `syncd`. The build sync client
waits for `/workspace/.zeta-ready` before the build starts.

## curl examples

```bash
BASE=http://zeta.local

curl -c cookies.txt \
  -H 'Content-Type: application/json' \
  -d '{"username":"dev@example.com","password":"dev"}' \
  "$BASE/api/login"

curl -b cookies.txt "$BASE/api/me"
curl -b cookies.txt "$BASE/api/profiles"

curl -b cookies.txt \
  -H 'Content-Type: application/json' \
  -d '{"profile":"cpp","repo":"git://git-server/cpp.git","commit":"main"}' \
  "$BASE/api/builds"

curl -b cookies.txt "$BASE/api/builds"
curl -b cookies.txt "$BASE/api/volumes"
curl -b cookies.txt "$BASE/api/builds/<id>/logs"
curl -b cookies.txt "$BASE/api/builds/<id>/metrics"
curl -b cookies.txt -o artifacts.tar "$BASE/api/builds/<id>/artifact"

# Local context build
curl -b cookies.txt \
  -F profile=go \
  -F sync=true \
  -F context=@context.tar.gz \
  "$BASE/api/builds"
```

If `zeta.local` is not in `/etc/hosts`, use the ingress IP with a Host header:

```bash
curl -H 'Host: zeta.local' http://<ADDRESS>/api/me
```

## Health

```http
GET /healthz
```

Returns `200 OK` when the portal is running.
