# ==========================================
# Stage 1: Build the Go binary
# ==========================================
FROM golang:alpine AS builder

# Enable automatic Go toolchain management
ENV GOTOOLCHAIN=auto

WORKDIR /build

# Install CA certificates and git
RUN apk add --no-cache ca-certificates git

# Cache Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy application source code
COPY . .

# Compile static Go binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /build/bot ./cmd/bot

# ==========================================
# Stage 2: Final Minimal Runtime Image
# ==========================================
FROM python:3.12-slim-bookworm AS runner

# Prevent Python from writing .pyc files and buffer stdout/stderr
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PYTHON_PATH=/usr/local/bin/python3

WORKDIR /app

# Install security certificates
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    curl \
    && rm -rf /var/lib/apt/lists/*

# Copy requirements and install Python dependencies for Pyrofork and Telethon
COPY workers/pyrofork/requirements.txt ./workers/pyrofork/requirements.txt
COPY workers/telethon/requirements.txt ./workers/telethon/requirements.txt

RUN pip install --no-cache-dir --root-user-action=ignore --upgrade pip && \
    pip install --no-cache-dir --root-user-action=ignore \
    -r ./workers/pyrofork/requirements.txt \
    -r ./workers/telethon/requirements.txt

# Copy Python worker scripts
COPY workers/pyrofork/worker.py ./workers/pyrofork/worker.py
COPY workers/telethon/worker.py ./workers/telethon/worker.py

# Copy compiled Go binary from builder stage
COPY --from=builder /build/bot /app/bot

# Create non-root system user for security
RUN groupadd -g 10001 appgroup && \
    useradd -u 10001 -g appgroup -s /bin/sh -m appuser && \
    chmod -R 755 /app && \
    chown -R appuser:appgroup /app

USER appuser

# Expose ports for HTTP health check (Render Web Service / Container probes)
EXPOSE 8080 10000

# Use exec form ENTRYPOINT for clean SIGTERM signal propagation
ENTRYPOINT ["/app/bot"]
