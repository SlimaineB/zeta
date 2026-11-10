#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TAG="${TAG:-0.1}"
cd "$ROOT"

docker build -t "zeta/cpp:${TAG}" -f images/cpp/Dockerfile .
docker build -t "zeta/python-uv:${TAG}" -f images/python-uv/Dockerfile .
docker build -t "zeta/go:${TAG}" -f images/go/Dockerfile .
docker build -t "zeta/git-server:${TAG}" -f images/git-server/Dockerfile .

docker pull busybox:1.36
docker pull alpine:3.20

for image in "zeta/cpp:${TAG}" "zeta/python-uv:${TAG}" "zeta/go:${TAG}" "zeta/git-server:${TAG}" busybox:1.36 alpine:3.20; do
  echo "importing $image"
  docker save "$image" | sudo -n k3s ctr images import -
done
