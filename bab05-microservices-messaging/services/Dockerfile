# Satu image untuk semua service Go bab 5 (hemat disk): binary dipilih lewat "command" di compose.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/ ./catalog ./order ./inventory ./payment ./notification ./monitor ./bank

FROM alpine:3.20
RUN adduser -D -u 10001 app
COPY --from=build /out/ /usr/local/bin/
USER app
