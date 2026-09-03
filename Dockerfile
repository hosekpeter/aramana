FROM golang:1.26-alpine AS builder
WORKDIR /src

# go.sum must be copied too, otherwise the build fails on missing checksums.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# The entry point lives in cmd/triage; the module root has no Go files.
# GOOS/GOARCH are left to the toolchain so the image builds on arm64 as well as amd64.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/triage-service ./cmd/triage

FROM alpine:3.21
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 triage
WORKDIR /app

# Migrations are embedded in the binary, so nothing else needs to be copied.
COPY --from=builder /out/triage-service /app/triage-service

USER triage
EXPOSE 8080
ENTRYPOINT ["/app/triage-service"]
