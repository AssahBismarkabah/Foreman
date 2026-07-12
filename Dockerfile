# Foreman -- Multi-stage Docker build
#
# Build:
#   docker build -t foreman:latest .
#
# The config file is NOT baked in -- mount it at runtime:
#   docker run -v ./foreman.yaml:/etc/foreman/foreman.yaml foreman:latest

# Stage 1: Build the Go binary
FROM golang:1.25-alpine AS builder

ARG VERSION=dev
WORKDIR /app

# Cache Go module downloads and build cache using BuildKit mounts.
# On subsequent builds these persist across invocations.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod/ \
    go mod download

# Copy source and build a static binary
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod/ \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build \
    -ldflags="-w -s -X main.version=${VERSION}" \
    -trimpath \
    -o /app/foreman \
    ./cmd/foreman

# Stage 2: Minimal runtime image
# distroless/static:nonroot includes:
#   - CA certificates
#   - Timezone data
#   - /etc/passwd with nonroot user (UID 65532)
# It does NOT include: shell, package manager, or any other binaries
FROM gcr.io/distroless/static:nonroot

# Copy the binary from the builder stage
COPY --from=builder /app/foreman /foreman

# The config file should be mounted at runtime:
#   -v ./foreman.yaml:/etc/foreman/foreman.yaml
# and started with:
#   foreman --config /etc/foreman/foreman.yaml

EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/foreman"]
CMD ["--config", "/etc/foreman/foreman.yaml"]
