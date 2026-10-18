FROM golang:1.26-alpine AS build

WORKDIR /src
ARG VERSION=local
COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/
# Static binary: the runtime image has no libc to link against.
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X main.buildVersion=${VERSION}" -o /out/torre ./cmd/torre

FROM alpine:3.21

WORKDIR /app
RUN apk add --no-cache tzdata ca-certificates \
    && adduser -D -H -u 10001 torre \
    && install -d -o torre -g torre -m 700 /data
COPY --from=build /out/torre /usr/local/bin/torre

# The followed flights live here; without the mount a redeploy forgets them.
VOLUME /data

ENV TORRE_LOG_JSON=1 \
    TORRE_DATA_DIR=/data \
    TORRE_PORT=8000

EXPOSE 8000
USER torre

HEALTHCHECK --interval=60s --timeout=10s --start-period=15s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8000/healthz >/dev/null || exit 1

ENTRYPOINT ["torre"]
