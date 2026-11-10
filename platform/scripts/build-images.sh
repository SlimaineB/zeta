#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

docker build -t zeta/cpp:0.1 -f images/cpp/Dockerfile .
docker build -t zeta/python-uv:0.1 -f images/python-uv/Dockerfile .
docker build -t zeta/go:0.1 -f images/go/Dockerfile .
docker build -t zeta/git-server:0.1 -f images/git-server/Dockerfile .

docker pull busybox:1.36
docker pull alpine:3.20

for image in zeta/cpp:0.1 zeta/python-uv:0.1 zeta/go:0.1 zeta/git-server:0.1 busybox:1.36 alpine:3.20; do
  echo "importing $image"
  docker save "$image" | sudo -n k3s ctr images import -
done
