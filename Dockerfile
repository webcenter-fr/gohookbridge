FROM --platform=$BUILDPLATFORM golang:latest
COPY . /go/src/github.com/webcenter-fr/gohookbridge
WORKDIR /go/src/github.com/webcenter-fr/gohookbridge
ARG TARGETARCH
RUN GOFLAGS="-buildvcs=false" CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a  -ldflags="-s -w"  -installsuffix cgo -o /tmp/gohookbridge .

FROM registry.access.redhat.com/ubi9/ubi-minimal
RUN microdnf -y update && microdnf -y --nodocs install tar rsync shadow-utils && microdnf clean all && useradd gohookbridge && rm -rf /var/cache/yum

COPY --from=0 /tmp/gohookbridge /usr/local/bin/gohookbridge

WORKDIR /home/gohookbridge
USER 1001
ENTRYPOINT ["/usr/local/bin/gohookbridge"]
