# --- Build Stage ---
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git

# Copy dependency manifests
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build Nautrouds Core and Compiler
RUN go build -o /bin/nautrouds ./cmd/core
RUN go build -o /bin/ntuc ./cmd/ntuc

# --- Final Stage ---
FROM alpine:3.21

# Install runtime dependencies: su-exec for privilege dropping, acl for fine-grained permissions
RUN apk add --no-cache su-exec acl

# Create non-root user for the service
RUN addgroup -S nautrouds && adduser -S nautrouds -G nautrouds

WORKDIR /app

# Copy binaries from builder
COPY --from=builder /bin/nautrouds /usr/local/bin/nautrouds
COPY --from=builder /bin/ntuc /usr/local/bin/ntuc

# Copy entrypoint script
COPY docker/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh

# Create initial directory structure
RUN mkdir -p /etc/nautrouds /var/run/nautrouds/services /var/run/nautrouds/entrypoints && \
    chown -R nautrouds:nautrouds /var/run/nautrouds /etc/nautrouds

# Define default volume for sockets
VOLUME ["/var/run/nautrouds"]

# Default environment variables
ENV NAUTROUDS_CONFIG=/etc/nautrouds/nautrouds.ntu
ENV NAUTROUDS_NTLC=ntuc
ENV NAUTROUDS_SERVICES_DIR=/var/run/nautrouds/services
ENV NAUTROUDS_ENTRYPOINT_DIR=/var/run/nautrouds/entrypoints

# The entrypoint script will handle final permission adjustments and drop privileges
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
