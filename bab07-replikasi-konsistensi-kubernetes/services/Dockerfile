# Satu image untuk semua service Go bab 7 (clocknode, raftnode, replapi, searchapi, k8swatch)
# agar layer dipakai bersama dan disk hemat. Image katalog untuk k3s dibuat sebagai tar
# (docker-archive) oleh mkimage, lalu disalin ke folder auto-import k3s oleh service k8s-loader.
FROM golang:1.24-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOTOOLCHAIN=local GOFLAGS=-trimpath
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    go build -ldflags="-s -w" -o /out/ ./cmd/... && \
    /out/mkimage -bin /out/katalog -o /out/katalog-image.tar -tag sister-bab07-katalog:v1 && \
    rm /out/mkimage

FROM alpine:3.20
RUN adduser -D -H -u 10001 app && mkdir -p /data /k3s-images && chown 10001 /data /k3s-images
COPY --from=build /out/katalog-image.tar /images/katalog-image.tar
COPY --from=build /out/clocknode /out/raftnode /out/replapi /out/searchapi /out/k8swatch /usr/local/bin/
USER 10001
EXPOSE 8080
