FROM alpine:3.20

RUN apk add --no-cache git git-daemon

ENV HOME=/tmp
USER 1000:1000

ENTRYPOINT ["git"]
