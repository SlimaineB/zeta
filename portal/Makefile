SHELL := /usr/bin/env bash
KUBE_CONTEXT ?= local
IMAGE ?= zeta/portal:0.1

.PHONY: build syncd test image import deploy logs

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o bin/zeta ./cmd/zeta

syncd:
	cd ../cli && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o ../portal/bin/syncd ./cmd/syncd

test:
	go test ./...

image: build syncd
	docker build -t $(IMAGE) .

import: image
	docker save $(IMAGE) | sudo -n k3s ctr images import -

deploy: import
	kubectl --context $(KUBE_CONTEXT) apply -f ../platform/manifests/namespace.yaml
	kubectl --context $(KUBE_CONTEXT) apply -f ../platform/manifests/context-pvc.yaml
	kubectl --context $(KUBE_CONTEXT) apply -f ../platform/manifests/portal.yaml
	kubectl --context $(KUBE_CONTEXT) -n zeta-poc rollout restart deploy/zeta-portal
	kubectl --context $(KUBE_CONTEXT) -n zeta-poc rollout status deploy/zeta-portal --timeout=180s

logs:
	kubectl --context $(KUBE_CONTEXT) -n zeta-poc logs deploy/zeta-portal -f
