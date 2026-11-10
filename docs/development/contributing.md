# Contributing

## Prerequisites

- Go 1.26+
- Docker
- k3s and `kubectl --context local`
- `make`
- Python 3.10+ for the documentation

## Build and test

```bash
cd portal
go test ./...
go vet ./...

cd ../cli
go test ./...
go vet ./...

cd ../platform
make images
```

Build the portal and deploy:

```bash
cd portal
make test
make deploy
```

Build the CLI:

```bash
cd cli
make build
./bin/zeta help
```

## Make targets

```bash
make doctor
make install-smart          # build images, import into k3s, Helm install
make install-platform       # raw manifests (development)
make install-platform WITH_GIT_SERVER=1
make install-user           # build the CLI from source
make install-cli            # install the latest release binary
make install                # install-smart + install-user
make status
make uninstall-platform
make helm-lint
make helm-template
make helm-package
```

Platform targets:

```bash
cd platform
make images
make git-init
make cpp
make python
make go
make all
make git-all
make cache-reset
make clean
```

## Adding a build profile

1. Create an image under `platform/images/<name>/`.
2. Add a build target and import step in `platform/scripts/build-images.sh`.
3. Add a profile to `portal/internal/builds/defaults.yaml`.
4. Document the profile in `docs/user-guide/profiles.md`.
5. Test with `zeta profiles` and a small build.
6. Run `go test ./...` and `go vet ./...`.

A profile is just YAML: name, language, image, commands, artifact path, cache,
resources, env, and metrics.

## Code style

- Run `gofmt` on Go files.
- Keep packages focused; the portal is split into `auth`, `builds`, `config`,
  `domain`, `httpapi`, `kube`, `sync`, and `ui`.
- Prefer small, testable functions.
- Add tests for behavior changes, especially around resource creation and API
  handlers.
- Do not log secrets.
- Keep user-facing messages consistent.

## Documentation

The docs are split into `docs/platform/` (Helm, operations) and
`docs/developer/` (CLI, first build). Keep the two audiences separate and make
sure cross-links resolve.

The docs use MkDocs Material:

```bash
python3 -m venv .venv-docs
. .venv-docs/bin/activate
pip install -r requirements-docs.txt
mkdocs serve
```

Build with strict links:

```bash
mkdocs build --strict
```

The GitHub Actions workflow publishes to `gh-pages` on pushes to `main`.

## Pull request checklist

- [ ] `go test ./...` passes in `portal` and `cli`.
- [ ] `go vet ./...` passes in `portal` and `cli`.
- [ ] `mkdocs build --strict` passes if docs changed.
- [ ] New behavior is documented.
- [ ] No secrets, kubeconfigs, or tokens are committed.
- [ ] The change is tested on a local k3s cluster when it touches Kubernetes
      resources.
- [ ] Commits have a clear, focused message.
