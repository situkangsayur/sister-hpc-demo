# Build multi-stage: biner lab statik + biner racedemo yang dibangun dengan -race (butuh cgo).
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lab . \
 && apk add --no-cache gcc musl-dev \
 && CGO_ENABLED=1 go build -race -trimpath -o /out/racedemo ./cmd/racedemo

FROM alpine:3.20
RUN adduser -D -u 10001 lab
COPY --from=build /out/lab /out/racedemo /app/
USER lab
EXPOSE 8080
ENTRYPOINT ["/app/lab"]
