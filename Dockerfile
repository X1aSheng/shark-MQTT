# Stage 1: Build
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o shark-mqtt ./cmd/

# Stage 2: Runtime
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /build/shark-mqtt /usr/local/bin/shark-mqtt

EXPOSE 1883 8883 9090

HEALTHCHECK --interval=15s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:9090/healthz || exit 1

ENTRYPOINT ["shark-mqtt"]
CMD ["-addr=:1883"]
