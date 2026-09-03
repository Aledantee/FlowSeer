# Dockerfile for the netpen t1 ring containers.
# Builds the netpen binary inside the container (linux/amd64), then runs
# it. The image is used for both the crafter and decoder containers.
FROM golang:1.26-bookworm AS builder
WORKDIR /src
COPY . .
WORKDIR /src/src/edge/netpen
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /netpen ./cmd/netpen

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    iproute2 tcpdump && rm -rf /var/lib/apt/lists/*
COPY --from=builder /netpen /usr/local/bin/netpen
ENTRYPOINT ["netpen"]
