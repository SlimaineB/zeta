# `zeta` CLI

The CLI is the developer entry point. It packages the context, calls the portal
API, streams logs, downloads artifacts, and manages persistent environments.

## Local configuration

The session is stored in `~/.config/zeta/config.json` with mode `0600`.
`zeta logout` only removes the local session.

| Variable | Purpose |
|---|---|
| `ZETA_SERVER` | Portal URL, for example `http://zeta.local` |
| `ZETA_COOKIE` | Session cookie (raw value or full cookie) |
| `ZETA_PROFILE` | Default profile for `zeta build` |

## Commands

| Command | Purpose |
|---|---|
| `zeta login` | Log in (dev mode or OIDC cookie) |
| `zeta logout` | Remove the local session |
| `zeta profiles` | List available profiles |
| `zeta build` | Run a local or Git build |
| `zeta builds` | List builds |
| `zeta status ID` | Show build details |
| `zeta logs ID` | Stream build logs |
| `zeta metrics ID` | Show cache metrics |
| `zeta artifacts ID` | Download build artifacts |
| `zeta volumes` | List managed volumes |
| `zeta volumes resize NAME SIZE` | Expand a volume |
| `zeta volumes delete NAME` | Delete an idle volume |
| `zeta init` | Provision a persistent environment |
| `zeta activate` | Start it, open a shell, and sync files |
| `zeta exec` | Run a command or interactive shell in an environment |
| `zeta port-forward` | Expose an environment port on localhost |
| `zeta deactivate` | Scale it to zero |
| `zeta ls` | List environments |
| `zeta rm` | Delete an environment and its volume |

The aliases `zeta up` / `zeta down` map to `activate` / `deactivate`.
`zeta env <init|activate|deactivate|ls|rm>` is still accepted for compatibility.

## Authentication

### Development mode

```bash
zeta login \
  --server http://zeta.local \
  --email dev@example.com \
  --password dev
```

| Flag | Description |
|---|---|
| `--server` | Portal URL (or `ZETA_SERVER`) |
| `--email` | Email (dev mode) |
| `--password` | Password (dev mode) |
| `--cookie` | Existing session cookie (OIDC mode) |

If `--email` or `--password` is omitted, the CLI prompts for it.

### OIDC mode

Browser integration is not available in the CLI yet. Copy the `zeta_session`
cookie from the browser, then:

```bash
zeta login --server https://zeta.example.com --cookie '<cookie_value>'
```

## Profiles

```bash
zeta profiles
```

Shows the name, language, default repository, and description of each profile.
See [Profiles](profiles.md).

## Build

### Local build (default)

```bash
cd my-project
zeta build
```

The CLI:

1. detects the profile from `CMakeLists.txt`, `go.mod`, or
   `pyproject.toml` / `setup.py` / `requirements.txt`;
2. creates a filtered `tar.gz` of the current directory;
3. uploads it to the portal with the session cookie;
4. streams logs live;
5. waits for the build to finish;
6. downloads and extracts artifacts into `artifacts/<id>`.

### `zeta build` flags

| Flag | Default | Description |
|---|---|---|
| `--profile NAME` | auto | Force the profile |
| `--context DIR` | `.` | Local directory to upload |
| `--exclude PATTERN` | — | Context exclusion (repeatable) |
| `--artifact-exclude PATTERN` | — | Downloaded artifact exclusion (repeatable) |
| `--git` | `false` | Enable Git mode |
| `--repo URL` | — | Repository URL (enables Git mode) |
| `--commit REF` | current branch | Branch, tag, or SHA |
| `--follow` | `true` | Follow logs live |
| `--artifacts` | `true` | Download and extract artifacts |
| `--artifacts-dir DIR` | `artifacts` | Artifact output directory |
| `--sync` | `false` | Bidirectional sync during the build |
| `--timeout DURATION` | `30m` | Build wait timeout |

Constraints:

- `--context` and `--git` / `--repo` are incompatible.
- `--commit` requires `--git` or `--repo`.
- `--sync` is incompatible with Git mode.

### Git build

```bash
zeta build --git --profile go \
  --repo git://git-server/go.git \
  --commit main
```

The commit must already be pushed and reachable from the cluster. A local
unpushed commit cannot be cloned by the Job.

### Typical output

```text
context: /home/user/my-project (27 KB)
build zeta-go-xxxxxxxxxxxx (go) context /home/user/my-project
--- logs ---
[ live build logs ]
build succeeded in 12.4 s
cache: 12 hits / 3 miss (80%) · 84 MB
artifacts: /home/user/my-project/artifacts/zeta-go-xxxxxxxxxxxx
```

### Interactive sync

```bash
zeta build --sync
```

The build waits for `/workspace/.zeta-ready`, syncs the initial context, then
exchanges local changes and pod-side files. A final flush collects the latest
artifacts before exit.

## Inspect builds

```bash
zeta builds
zeta status zeta-go-xxxxxxxxxxxx
zeta logs zeta-go-xxxxxxxxxxxx
zeta metrics zeta-go-xxxxxxxxxxxx
zeta artifacts -o artifacts.tar zeta-go-xxxxxxxxxxxx
```

`zeta metrics` returns JSON:

```json
{
  "hits": 5,
  "misses": 1,
  "hitRate": 83.33,
  "sizeBytes": 43628
}
```

!!! tip "Flag order"
    Flags must come before the build ID: `zeta artifacts -o out.tar ID`,
    `zeta status --json ID`.

## Volumes

```bash
zeta volumes
zeta volumes --role cache
zeta volumes --older-than 720h
zeta volumes --json
zeta volumes resize zeta-poc-cache 5Gi
zeta volumes delete zeta-test-cache
```

`--older-than` is a reporting filter: Zeta does not delete volumes
automatically. A volume in use by a build cannot be resized or deleted.

See [Volumes and artifacts](volumes.md).

## Persistent environments

```bash
zeta init
zeta activate
zeta ls
zeta deactivate
zeta rm
```

| Command | Flags |
|---|---|
| `zeta init` | `--name`, `--profile`, `--context`, `--size` |
| `zeta activate` | `--name`, `--context`, `--exclude` (repeatable) |
| `zeta exec` | `--name`, `--context`, `-i`, `-t`, `-it`, `-- COMMAND...` |
| `zeta port-forward` | `--name`, `--context`, `-p [LOCAL:]REMOTE` (repeatable), `--address` |
| `zeta code` | `--name`, `--context`, `--print` |
| `zeta share` | `--name`, `--context`, `--ttl`, `--label` |
| `zeta share list` | `--name`, `--context`, `--json` |
| `zeta share revoke` | `SHARE_ID`, `--name`, `--context` |
| `zeta deactivate` | `--name`, `--context` |
| `zeta ls` | `--json` |
| `zeta rm` | `[NAME]`, `--name`, `--context` |

The name is resolved in this order: `--name` flag, `name` field in `zeta.yaml`,
then the context directory name. It is normalized to a slug (lowercase letters,
digits, and dashes, up to 40 characters).

`zeta init`, `activate`, `code`, `deactivate`, and `rm` read `zeta.yaml` from
`--context` (default `.`). Flags take precedence over the file.

`zeta code` starts the environment, enables the `code-server` sidecar if the
environment predates it, and opens the authenticated browser IDE on
`/workspace`. Use `--print` on headless machines and open the printed URL
manually.

`zeta share` creates a temporary link with full workspace access. The default
lifetime is `1h`; use `--ttl` for another duration and `--label` to identify the
guest in the dashboard. `zeta share list` shows active links and connected
guests; `zeta share revoke SHARE_ID` invalidates a link immediately.

See [Persistent environments](environments.md) and
[`zeta.yaml` reference](../reference/zeta-yaml.md).

## Run commands and forward ports

`zeta exec` runs a command inside an environment without opening the full
mirrored shell. It starts the environment first if needed and returns the
command's exit code.

```bash
zeta exec -- go test ./...
zeta exec -it                       # interactive shell
zeta exec -it -- bash -l            # explicit shell
```

| Flag | Meaning |
|---|---|
| `-i` | Keep stdin attached |
| `-t` | Allocate a pseudo-TTY |
| `-it` | Both (Docker-style shorthand) |

Without a command, `zeta exec` opens an interactive shell. Commands run from
`/workspace`.

`zeta port-forward` tunnels a TCP port from the environment to your machine:

```bash
zeta port-forward -p 8080:80
zeta port-forward -p 3000 -p 5432:5432 --address 0.0.0.0
```

`[LOCAL:]REMOTE` follows the Docker convention; `REMOTE` alone binds the same
port locally. The default bind address is `127.0.0.1`; pass `--address 0.0.0.0`
to expose it on your LAN. Each accepted local connection opens a fresh tunnel to
the environment pod, so HTTP keep-alive and WebSocket clients both work.

See [Coming from Docker or kubectl](docker-users.md) for a side-by-side cheat
sheet.

## Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | CLI error, failed build, missing session, or rejected request |

Errors are written to `stderr` with the `zeta:` prefix.

## Help

```bash
zeta help
zeta --help
```
