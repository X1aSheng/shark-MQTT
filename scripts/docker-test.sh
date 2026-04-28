#!/usr/bin/env bash
# Docker smoke test for shark-mqtt
#
# Builds the image, starts a container, verifies the health endpoint, and
# runs an MQTT connect/publish/subscribe test against the container.
#
# Usage:
#   bash scripts/docker-test.sh

set -euo pipefail

IMAGE="shark-mqtt:test"
CONTAINER="shark-mqtt-smoke"
MQTT_PORT=11883
HEALTH_PORT=19090

echo "=== shark-mqtt Docker smoke test ==="
echo ""

# Cleanup from previous runs
docker rm -f "$CONTAINER" 2>/dev/null || true

echo "[1/4] Building Docker image..."
docker build -t "$IMAGE" .

echo ""
echo "[2/4] Starting container..."
docker run -d \
    --name "$CONTAINER" \
    -p "$MQTT_PORT:1883" \
    -p "$HEALTH_PORT:9090" \
    "$IMAGE"

# Wait for healthy
echo "    Waiting for health check..."
for i in $(seq 1 30); do
    if curl -sf "http://localhost:$HEALTH_PORT/healthz" >/dev/null 2>&1; then
        echo "    Container is healthy."
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "    ERROR: Container did not become healthy after 30s."
        docker logs "$CONTAINER"
        docker rm -f "$CONTAINER"
        exit 1
    fi
    sleep 1
done

echo ""
echo "[3/4] Running MQTT smoke test..."
if go run scripts/mqtt_smoke.go -addr "localhost:$MQTT_PORT"; then
    echo ""
else
    echo ""
    echo "ERROR: MQTT smoke test failed."
    docker logs "$CONTAINER"
    docker rm -f "$CONTAINER"
    exit 1
fi

echo ""
echo "[4/4] Cleanup..."
docker rm -f "$CONTAINER"
echo "    Container removed."

echo ""
echo "=== Smoke test passed ==="
