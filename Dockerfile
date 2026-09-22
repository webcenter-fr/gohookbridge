# Defaults so the Dockerfile also builds under plain BuildKit/Dagger without
# buildx's automatic platform args (buildx overrides these anyway).
ARG BUILDPLATFORM=linux/amd64

FROM --platform=$BUILDPLATFORM node:22-alpine AS webbuild
WORKDIR /src
COPY internal/web/ ./internal/web/
COPY web/ ./web/
WORKDIR /src/web
RUN npm ci && npm run build          # emits /src/internal/web/static
# Guard: fail the build if the static output is missing (catches nuxt generate failures)
RUN test -f /src/internal/web/static/index.html || (echo "ERROR: static/index.html missing after nuxt generate" && exit 1)

FROM --platform=$BUILDPLATFORM golang:latest AS builder
COPY . /go/src/github.com/webcenter-fr/gohookbridge
COPY --from=webbuild /src/internal/web/static /go/src/github.com/webcenter-fr/gohookbridge/internal/web/static
WORKDIR /go/src/github.com/webcenter-fr/gohookbridge
ARG TARGETARCH=amd64
ARG VERSION=dev
# Inject the resolved version into the embedded version file so the binary's
# /version endpoint reports the exact image tag (mirrors the goreleaser hook).
RUN printf '%s' "$VERSION" > /go/src/github.com/webcenter-fr/gohookbridge/internal/app/templates/version
RUN GOFLAGS="-buildvcs=false" CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a -ldflags="-s -w" -installsuffix cgo -o /tmp/gohookbridge ./cmd/gohookbridge

FROM registry.access.redhat.com/ubi9/ubi-minimal
RUN microdnf -y update && microdnf -y --nodocs install tar rsync shadow-utils && microdnf clean all && useradd gohookbridge && rm -rf /var/cache/yum

COPY --from=builder /tmp/gohookbridge /usr/local/bin/gohookbridge

WORKDIR /home/gohookbridge
USER 1001
ENTRYPOINT ["/usr/local/bin/gohookbridge"]
