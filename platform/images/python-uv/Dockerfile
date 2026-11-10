FROM python:3.12-slim

COPY --from=ghcr.io/astral-sh/uv:0.7.17 /uv /uvx /usr/local/bin/

RUN pip install --no-cache-dir hatchling \
 && useradd --create-home --uid 1000 builder

ENV UV_LINK_MODE=copy \
    UV_COMPILE_BYTECODE=1 \
    UV_PYTHON_DOWNLOADS=never

USER 1000
WORKDIR /workspace
