FROM golang:1.27-bookworm AS builder

WORKDIR /src
COPY . .
WORKDIR /src/src/edge/netpen
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -o /netpen ./cmd/netpen

FROM quay.io/frrouting/frr:10.1.0

COPY --from=builder /netpen /usr/local/bin/netpen
