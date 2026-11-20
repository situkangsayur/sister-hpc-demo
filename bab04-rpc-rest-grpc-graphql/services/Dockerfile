# Satu Dockerfile untuk dua service Go (katalog dan klien); TARGET memilih folder.
FROM golang:1.24-alpine AS bangun
ARG TARGET=katalog
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app ./${TARGET}

FROM alpine:3.20
COPY --from=bangun /app /app
USER 65534
ENTRYPOINT ["/app"]
