# syntax=docker/dockerfile:1
# Multi-architecture image: linux/amd64, linux/arm64 (Raspberry Pi 4/5), linux/arm/v7 (older Pi).

FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETOS TARGETARCH TARGETVARIANT
ARG VERSION=dev
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags "-s -w -X github.com/blamebox/blamebox/internal/monitor.Version=${VERSION}" \
    -o /out/blamebox ./cmd/blamebox

FROM alpine:3.20 AS certs
RUN apk add --no-cache ca-certificates

FROM scratch
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/blamebox /blamebox
ENV BLAMEBOX_DATA_DIR=/data \
    BLAMEBOX_LISTEN=:8080
VOLUME /data
EXPOSE 8080
HEALTHCHECK --interval=1m --timeout=5s --start-period=30s CMD ["/blamebox", "-healthcheck"]
ENTRYPOINT ["/blamebox"]
