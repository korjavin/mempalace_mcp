#!/usr/bin/env bash
# Simple integration test: build Docker image and verify /health responds.
set -euo pipefail

IMAGE_NAME="mempalace-mcp-test"
CONTAINER_NAME="mempalace-mcp-test-$$"
PORT=18080

cleanup() {
    echo "Cleaning up..."
    docker rm -f "$CONTAINER_NAME" 2>/dev/null || true
    docker rmi "$IMAGE_NAME" 2>/dev/null || true
}
trap cleanup EXIT

echo "Building Docker image..."
docker build -t "$IMAGE_NAME" .

echo "Starting container..."
docker run -d --name "$CONTAINER_NAME" \
    -p "$PORT:8080" \
    -e GOOGLE_CLIENT_ID=test \
    -e GOOGLE_CLIENT_SECRET=test \
    -e COOKIE_SECRET=test-secret-that-is-32-bytes!! \
    -e ADMIN_EMAILS=test@example.com \
    "$IMAGE_NAME"

echo "Waiting for container to start..."
for i in $(seq 1 30); do
    if curl -sf "http://localhost:$PORT/health" > /dev/null 2>&1; then
        echo "Health check passed after ${i}s"
        exit 0
    fi
    sleep 1
done

echo "FAIL: /health did not respond within 30s"
docker logs "$CONTAINER_NAME"
exit 1
