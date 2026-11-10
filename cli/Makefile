SHELL := /usr/bin/env bash

.PHONY: build test install

build:
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/zeta ./cmd/zeta

test:
	go test ./...

install: build
	install -d $(HOME)/.local/bin
	install -m 0755 bin/zeta $(HOME)/.local/bin/zeta
