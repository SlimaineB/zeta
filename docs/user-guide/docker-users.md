# Coming from Docker or kubectl

Zeta keeps the commands you already know, but the mental model is slightly
different:

| Docker / kubectl | Zeta | Notes |
|---|---|---|
| image | profile | `zeta init --profile go` |
| container | environment | one persistent pod + PVC |
| `docker run -it IMAGE` | `zeta init` then `zeta activate` | files are mirrored automatically |
| `docker exec -it CONTAINER sh` | `zeta exec -it` | interactive shell in `/workspace` |
| `docker exec CONTAINER CMD` | `zeta exec -- CMD` | returns the command exit code |
| `docker run -p 8080:80` | `zeta port-forward -p 8080:80` | TCP only, one tunnel per connection |
| `docker ps` | `zeta ls` | |
| `docker stop` | `zeta deactivate` | scales the pod to zero, keeps the volume |
| `docker rm` | `zeta rm` | deletes the environment and its workspace |
| `docker logs` | `zeta logs ID` | build logs |
| `docker cp` | automatic sync | `zeta activate` mirrors both ways |
| `kubectl exec POD -- CMD` | `zeta exec -- CMD` | |
| `kubectl port-forward POD 8080:80` | `zeta port-forward -p 8080:80` | |
| `kubectl get pods` | `zeta ls` | |
| `kubectl delete pod POD` | `zeta deactivate` / `zeta rm` | |
| `-v /host:/container` | not supported | use the synced `/workspace` or `zeta volumes` |
| `-e KEY=VALUE` | `env:` in `zeta.yaml` | no `-e` flag yet |
| `-w /path` | commands run in `/workspace` | use `zeta exec -- sh -c 'cd sub && ...'` |

## Interactive shell

```bash
zeta exec -it
```

`-i` keeps stdin attached, `-t` allocates a pseudo-TTY, and `-it` combines
both, exactly like Docker. Without a command, `zeta exec` opens an interactive
shell.

## Run one command

```bash
zeta exec -- go test ./...
zeta exec -it -- bash -l
```

The exit code of the remote command is returned by the CLI, so it composes with
shell scripts and CI:

```bash
if zeta exec -- make check; then
  echo "checks passed"
fi
```

## Publish a port

```bash
zeta port-forward -p 8080:80
zeta port-forward -p 3000 -p 5432:5432
```

`[LOCAL:]REMOTE` follows the Docker convention. The default bind address is
`127.0.0.1`; use `--address 0.0.0.0` to expose the port on your LAN.

Unlike `docker run -p`, forwarding is attached to your CLI session, not to the
environment. Stop the command with `Ctrl-C` to close the tunnels; the
environment keeps running.

## What is intentionally different

- **No bind mounts.** The workspace is a Kubernetes PVC mirrored by
  `zeta activate`. Use `zeta volumes` to manage caches and artifacts.
- **No local daemon.** Builds run as Kubernetes Jobs and environments as
  Deployments, so there is no `dockerd` on your machine.
- **TCP only.** `-p` forwards TCP; UDP is not supported.
- **Quotas.** Per-user build and environment limits may return `429`; see
  [Portal configuration](../reference/configuration.md#fair-use-and-parallel-tasks).
