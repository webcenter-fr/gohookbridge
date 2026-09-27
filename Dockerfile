# Defaults so the Dockerfile also builds under plain BuildKit/Dagger without
# buildx's automatic platform args (buildx overrides these anyway).
ARG BUILDPLATFORM=linux/amd64

# Base images are pinned by digest for supply-chain integrity (SEC-016).
# Refresh a digest with: docker buildx imagetools inspect <image>:<tag>
# update: docker buildx imagetools inspect node:22-alpine
FROM --platform=$BUILDPLATFORM node:22-alpine@sha256:0a7108bf6c7bf5de370ffb1a3ed6be93d405b43ff159f681a8d18c0e2bc2e402 AS webbuild
WORKDIR /src
COPY internal/web/ ./internal/web/
COPY web/ ./web/
WORKDIR /src/web
RUN npm ci && npm run build          # emits /src/internal/web/static
# Guard: fail the build if the static output is missing (catches nuxt generate failures)
RUN test -f /src/internal/web/static/index.html || (echo "ERROR: static/index.html missing after nuxt generate" && exit 1)

# update: docker buildx imagetools inspect golang:1.27
FROM --platform=$BUILDPLATFORM golang:1.27@sha256:3680233e3204827fbdc66088528ae6d4b3d034f51d03a99d454f6de034888244 AS builder
COPY . /go/src/github.com/webcenter-fr/gohookbridge
COPY --from=webbuild /src/internal/web/static /go/src/github.com/webcenter-fr/gohookbridge/internal/web/static
WORKDIR /go/src/github.com/webcenter-fr/gohookbridge
ARG TARGETARCH=amd64
ARG VERSION=dev
# Inject the resolved version into the embedded version file so the binary's
# /version endpoint reports the exact image tag (mirrors the goreleaser hook).
RUN printf '%s' "$VERSION" > /go/src/github.com/webcenter-fr/gohookbridge/internal/app/templates/version
RUN GOFLAGS="-buildvcs=false" CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a -ldflags="-s -w" -installsuffix cgo -o /tmp/gohookbridge ./cmd/gohookbridge

# update: docker buildx imagetools inspect registry.access.redhat.com/ubi9/ubi-minimal
FROM registry.access.redhat.com/ubi9/ubi-minimal@sha256:8ebe2ad8fdf3cab3e5a53c1edc69194c98209cfadab24b884f4ad9ebcf7bbbfc
RUN microdnf -y update && microdnf -y --nodocs install tar rsync shadow-utils && microdnf clean all && useradd gohookbridge && rm -rf /var/cache/yum

COPY --from=builder /tmp/gohookbridge /usr/local/bin/gohookbridge

WORKDIR /home/gohookbridge
USER 1001
ENTRYPOINT ["/usr/local/bin/gohookbridge"]
