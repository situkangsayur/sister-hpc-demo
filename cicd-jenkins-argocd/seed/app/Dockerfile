# Multi-stage: target "test" dipakai Jenkins pada tahap Unit test; target akhir adalah image
# runtime. BuildKit hanya membangun stage yang dibutuhkan target, dan stage "dasar" di-cache.
FROM golang:1.24-alpine AS dasar
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod ./
COPY *.go ./

FROM dasar AS test
RUN go vet ./... && go test -v ./...

FROM dasar AS build
ARG VERSI=dev
RUN go build -trimpath -ldflags="-s -w -X main.versi=${VERSI}" -o /out/app .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/app /app
USER nonroot
EXPOSE 8080
ENTRYPOINT ["/app"]
