# Satu image untuk semua service Go Bab 2 (echo, router, prober, chat, chatgw):
# layer dipakai bersama sehingga hemat disk. Runtime alpine karena butuh
# iproute2 (tc, ip, ss) dan tcpdump.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./echo ./router ./prober ./chat ./chatgw

FROM alpine:3.20
ENV TZ=Asia/Jakarta
RUN apk add --no-cache iproute2 tcpdump \
    && addgroup -S app && adduser -S -G app app
COPY --from=build /out/ /app/
# echo, router, dan prober butuh root + NET_ADMIN/NET_RAW (tc, ip route, tcpdump);
# chat dan chatgw dijalankan sebagai user app lewat compose.
ENTRYPOINT []
