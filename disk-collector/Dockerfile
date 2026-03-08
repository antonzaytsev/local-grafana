FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod main.go .
RUN CGO_ENABLED=0 go build -o collector .

FROM alpine:3.19
RUN apk add --no-cache smartmontools ca-certificates
WORKDIR /app
COPY --from=builder /app/collector .
CMD ["./collector"]
