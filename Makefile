SHELL := /usr/bin/env bash
KUBE_CONTEXT ?= local
IMAGE ?= zeta/portal:0.1
WITH_GIT_SERVER ?= 0

.PHONY: help install install-platform install-user uninstall-platform doctor status

help:
	@printf '%s\n' \
	  'Zeta' \
	  '' \
	  '  make install-platform     Installe la plateforme dans k3s (images + portail)' \
	  '  make install-user         Installe le client zeta dans ~/.local/bin' \
	  '  make install              Fait les deux' \
	  '  make doctor               Vérifie les prérequis' \
	  '  make status               Affiche l’état de la plateforme' \
	  '  make uninstall-platform   Supprime le namespace zeta-poc' \
	  '' \
	  'Variables:' \
	  '  KUBE_CONTEXT=local        Contexte kubectl (défaut: local)' \
	  '  IMAGE=zeta/portal:0.1 Image du portail' \
	  '  WITH_GIT_SERVER=1         Déploie aussi le serveur Git d’exemple'

install: install-platform install-user

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
