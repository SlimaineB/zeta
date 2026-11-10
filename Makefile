SHELL := /usr/bin/env bash
KUBE_CONTEXT ?= local
IMAGE ?= zeta/portal:0.1
WITH_GIT_SERVER ?= 0
SMART_ARGS ?=

ifeq ($(WITH_GIT_SERVER),1)
SMART_ARGS += --with-git-server
endif

.PHONY: help install install-smart install-cli install-platform install-user uninstall-platform doctor status helm-lint helm-template helm-package

help:
	@printf '%s\n' \
	  'Zeta' \
	  '' \
	  '  make install              Installe la plateforme (Helm) et le client' \
	  '  make install-smart        Installe ou met à jour la plateforme avec Helm' \
	  '  make install-cli          Installe le client depuis la dernière release GitHub' \
	  '  make install-platform     Installe la plateforme depuis les manifests locaux (dev)' \
	  '  make install-user         Compile et installe le client dans ~/.local/bin' \
	  '  make doctor               Vérifie les prérequis' \
	  '  make status               Affiche l’état de la plateforme' \
	  '  make uninstall-platform   Supprime le namespace zeta-poc' \
	  '  make helm-lint            Valide le chart Helm' \
	  '  make helm-template        Affiche les manifests rendus par Helm' \
	  '  make helm-package         Crée dist/zeta-<version>.tgz' \
	  '' \
	  'Variables:' \
	  '  KUBE_CONTEXT=local        Contexte kubectl (défaut: local)' \
	  '  IMAGE=zeta/portal:0.1     Image du portail (install-platform)' \
	  '  WITH_GIT_SERVER=1         Déploie aussi le serveur Git d’exemple' \
	  '  SMART_ARGS="..."          Options supplémentaires pour scripts/install.sh'

install: install-smart install-user

install-smart:
	KUBE_CONTEXT=$(KUBE_CONTEXT) ./scripts/install.sh $(SMART_ARGS)

install-cli:
	curl -fsSL https://raw.githubusercontent.com/SlimaineB/zeta/main/scripts/install-cli.sh | sh

install-platform:
	KUBE_CONTEXT=$(KUBE_CONTEXT) IMAGE=$(IMAGE) WITH_GIT_SERVER=$(WITH_GIT_SERVER) ./scripts/install-platform.sh

install-user:
	./scripts/install-user.sh

uninstall-platform:
	KUBE_CONTEXT=$(KUBE_CONTEXT) ./scripts/uninstall-platform.sh

doctor:
	KUBE_CONTEXT=$(KUBE_CONTEXT) ./scripts/doctor.sh

status:
	kubectl --context $(KUBE_CONTEXT) -n zeta-poc get deploy,svc,ingress,pods

helm-lint:
	helm lint charts/zeta

helm-template:
	helm template zeta charts/zeta --namespace zeta-poc

helm-package:
	mkdir -p dist
	helm package charts/zeta --destination dist
