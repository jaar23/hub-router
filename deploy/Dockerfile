# syntax=docker/dockerfile:1

# ── Stage 1: build ────────────────────────────────────────────────────────────
FROM golang:1.22-alpine AS builder

# Install ca-certificates so TLS works in the final image.
RUN apk add --no-cache ca-certificates tzdata

WORKDIR /src

# Cache dependencies separately from source code.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build a fully static binary.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w -extldflags '-static'" \
    -o /hub-router ./cmd/hub-router

# ── Stage 2: minimal runtime ──────────────────────────────────────────────────
# distroless/static-debian12 is ~2MB, has no shell, no package manager,
# and runs as non-root (uid=65532) by default.
FROM gcr.io/distroless/static-debian12:nonroot

# Copy ca-certificates and timezone data from the builder.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo

# Copy the static binary.
COPY --from=builder /hub-router /hub-router

# Document the default port.
EXPOSE 8080

# Built-in healthcheck: the same binary runs with -healthcheck flag,
# connects to localhost, and exits 0/1. No shell or wget required.
HEALTHCHECK --interval=15s --timeout=5s --start-period=5s --retries=3 \
    CMD ["/hub-router", "-healthcheck"]

# Non-root user (uid 65532 = "nonroot" in distroless).
USER nonroot:nonroot

ENTRYPOINT ["/hub-router"]
