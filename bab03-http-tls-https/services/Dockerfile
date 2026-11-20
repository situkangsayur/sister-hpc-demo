# Satu image untuk tiga binary Go Bab 3 (certgen, origin, probe) agar hemat disk.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.work ./
COPY certgen/go.mod certgen/
COPY origin/go.mod origin/go.sum origin/
COPY probe/go.mod probe/go.sum probe/
RUN go mod download -C origin && go mod download -C probe
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/certgen ./certgen && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/origin ./origin && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/probe ./probe

FROM alpine:3.20
# iproute2-tc: perintah `tc` untuk netem (delay/loss buatan) di container probe.
RUN apk add --no-cache iproute2-tc
COPY --from=build /out/ /usr/local/bin/
ENTRYPOINT []
