FROM alpine:3.20

RUN apk add --no-cache ca-certificates

COPY bin/zeta /zeta
COPY bin/syncd /syncd

USER 65532:65532

ENTRYPOINT ["/zeta"]
