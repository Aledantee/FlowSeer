# FlowSeer t1 gNMI reference target: the standalone gnmitarget server
# built from this context (its own Go module, independent of the repo
# root module).
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum main.go ./
RUN go mod download && CGO_ENABLED=0 go build -o /gnmitarget .

FROM alpine:3.20
COPY --from=build /gnmitarget /gnmitarget
EXPOSE 9339
ENTRYPOINT ["/gnmitarget", "-addr", ":9339"]
