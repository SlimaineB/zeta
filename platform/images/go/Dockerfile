FROM golang:1.26-bookworm

RUN useradd --create-home --uid 1000 builder

ENV GOCACHE=/cache/go-build \
    GOMODCACHE=/cache/go-mod

USER 1000
WORKDIR /workspace
