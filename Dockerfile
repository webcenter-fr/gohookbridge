FROM --platform=$BUILDPLATFORM node:22-alpine AS webbuild
WORKDIR /src
COPY gohookbridge/web/ ./gohookbridge/web/
COPY web/ ./web/
WORKDIR /src/web
RUN npm ci && npm run build          # emits /src/gohookbridge/web/static
# Guard: fail the build if the static output is missing (catches nuxt generate failures)
RUN test -f /src/gohookbridge/web/static/index.html || (echo "ERROR: static/index.html missing after nuxt generate" && exit 1)

FROM --platform=$BUILDPLATFORM golang:latest AS builder
COPY . /go/src/github.com/webcenter-fr/gohookbridge
COPY --from=webbuild /src/gohookbridge/web/static /go/src/github.com/webcenter-fr/gohookbridge/gohookbridge/web/static
WORKDIR /go/src/github.com/webcenter-fr/gohookbridge
ARG TARGETARCH
RUN GOFLAGS="-buildvcs=false" CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a  -ldflags="-s -w"  -installsuffix cgo -o /tmp/gohookbridge ./cmd/gohookbridge

FROM registry.access.redhat.com/ubi9/ubi-minimal
RUN microdnf -y update && microdnf -y --nodocs install tar rsync shadow-utils && microdnf clean all && useradd gohookbridge && rm -rf /var/cache/yum

COPY --from=builder /tmp/gohookbridge /usr/local/bin/gohookbridge

WORKDIR /home/gohookbridge
USER 1001
ENTRYPOINT ["/usr/local/bin/gohookbridge"]
