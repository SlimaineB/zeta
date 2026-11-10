FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      build-essential \
      ca-certificates \
      ccache \
      cmake \
      curl \
      git \
      ninja-build \
      pkg-config \
 && rm -rf /var/lib/apt/lists/* \
 && useradd --create-home --uid 1000 builder

USER 1000
WORKDIR /workspace
