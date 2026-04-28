FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /shark-mqtt ./cmd/

FROM alpine:3.21
RUN apk --no-cache add ca-certificates tzdata wget
RUN addgroup -S shark && adduser -S shark -G shark

COPY --from=builder /shark-mqtt /usr/local/bin/shark-mqtt

USER shark

EXPOSE 1883/tcp 8883/tcp 9090/tcp

HEALTHCHECK --interval=15s --timeout=3s --retries=3 \
  CMD wget -qO- http://localhost:9090/healthz || exit 1

LABEL org.opencontainers.image.title="shark-mqtt"
LABEL org.opencontainers.image.description="High-performance MQTT 3.1.1 broker with cluster support"
LABEL org.opencontainers.image.source="https://github.com/X1aSheng/shark-mqtt"
LABEL org.opencontainers.image.licenses="MIT"

ENTRYPOINT ["shark-mqtt"]
CMD ["-addr=:1883"]
