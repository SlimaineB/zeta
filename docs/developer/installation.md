# Install the CLI

The `zeta` CLI is a single static binary. You do not need Go, Docker, or
`kubectl` to use it.

## Linux and macOS

```bash
curl -fsSL https://raw.githubusercontent.com/SlimaineB/zeta/main/scripts/install-cli.sh | sh
```

The installer:

1. detects Linux or macOS and `amd64` or `arm64`;
2. downloads `zeta-<os>-<arch>` from the latest GitHub Release;
3. verifies it against `checksums.txt`;
4. installs it to `~/.local/bin/zeta`.

Choose another directory or pin a version:

```bash
ZETA_BIN_DIR="$HOME/bin" \
  curl -fsSL https://raw.githubusercontent.com/SlimaineB/zeta/main/scripts/install-cli.sh | sh

ZETA_VERSION=0.1.0 \
  curl -fsSL https://raw.githubusercontent.com/SlimaineB/zeta/main/scripts/install-cli.sh | sh
```

If `~/.local/bin` is not on your `PATH`, the installer prints the line to add to
your shell profile.

## Windows

In PowerShell:

```powershell
irm https://raw.githubusercontent.com/SlimaineB/zeta/main/scripts/install-cli.ps1 | iex
```

The script installs `zeta.exe` to `%LOCALAPPDATA%\zeta\bin` and adds that
directory to your user `PATH`. Open a new terminal afterward.

Pinned version or custom directory:

```powershell
$env:ZETA_VERSION = "0.1.0"
$env:ZETA_BIN_DIR = "$HOME\bin"
irm https://raw.githubusercontent.com/SlimaineB/zeta/main/scripts/install-cli.ps1 | iex
```

The CLI works on Windows, but builds run in Linux pods. File sync, shells, and
artifact downloads behave the same as on Linux.

## Manual download

Every release contains:

| Asset | Platform |
|---|---|
| `zeta-linux-amd64` | Linux x86-64 |
| `zeta-linux-arm64` | Linux ARM64 |
| `zeta-darwin-amd64` | macOS Intel |
| `zeta-darwin-arm64` | macOS Apple silicon |
| `zeta-windows-amd64.exe` | Windows x86-64 |
| `zeta-windows-arm64.exe` | Windows ARM64 |
| `checksums.txt` | SHA-256 checksums |

Download from <https://github.com/SlimaineB/zeta/releases>. On Linux or macOS:

```bash
VERSION=v0.1.0
curl -fLO "https://github.com/SlimaineB/zeta/releases/download/${VERSION}/zeta-linux-amd64"
curl -fLO "https://github.com/SlimaineB/zeta/releases/download/${VERSION}/checksums.txt"
sha256sum --check --ignore-missing checksums.txt
install -m 0755 zeta-linux-amd64 ~/.local/bin/zeta
```

## Build from source

```bash
cd cli
make build
./bin/zeta help
```

Or install it with the repository helper:

```bash
make install-user
```

The source build requires Go 1.26 or newer. This is only needed for development;
users should prefer the release binaries.

## Verify

```bash
zeta help
```

`zeta help` lists the commands. If the shell cannot find the binary, check your
`PATH`.

## Log in

Development authentication:

```bash
zeta login --server http://zeta.local --email dev@example.com --password dev
```

The session is stored in `~/.config/zeta/config.json` with mode `0600`. The
server can also be set with `ZETA_SERVER`.

OIDC browser login is not integrated into the CLI yet. Copy the `zeta_session`
cookie from the browser and run:

```bash
zeta login --server https://zeta.example.com --cookie '<cookie_value>'
```

## Next steps

- [First build](../getting-started/quickstart.md)
- [CLI guide](../user-guide/cli.md)
- [Builds and sync](../user-guide/builds.md)
- [Persistent environments](../user-guide/environments.md)
