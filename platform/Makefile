SHELL := /usr/bin/env bash
KUBE_CONTEXT ?= local
export KUBE_CONTEXT

.PHONY: images cpp python go all git-init git-cpp git-python git-go git-all bench cache-reset clean

images:
	./scripts/build-images.sh

cpp:
	./scripts/run-build.sh cpp

python:
	./scripts/run-build.sh python

go:
	./scripts/run-build.sh go

all: images cpp python go

git-init:
	./scripts/init-git.sh

git-cpp:
	./scripts/run-build.sh cpp git

git-python:
	./scripts/run-build.sh python git

git-go:
	./scripts/run-build.sh go git

git-all: git-init git-cpp git-python git-go

bench:
	./scripts/benchmark.sh

cache-reset:
	kubectl --context $(KUBE_CONTEXT) -n zeta-poc delete job poc-cpp poc-python poc-go poc-cpp-git poc-python-git poc-go-git --ignore-not-found --wait=true
	kubectl --context $(KUBE_CONTEXT) -n zeta-poc delete pvc zeta-poc-cache --ignore-not-found --wait=true
	kubectl --context $(KUBE_CONTEXT) -n zeta-poc apply -f manifests/cache-pvc.yaml

clean:
	kubectl --context $(KUBE_CONTEXT) -n zeta-poc delete job poc-cpp poc-python poc-go --ignore-not-found
	kubectl --context $(KUBE_CONTEXT) -n zeta-poc delete pod artifact-server --ignore-not-found
	kubectl --context $(KUBE_CONTEXT) -n zeta-poc delete configmap ctx-cpp ctx-python ctx-go --ignore-not-found
	kubectl --context $(KUBE_CONTEXT) delete namespace zeta-poc --ignore-not-found
