FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/controlplane ./cmd/controlplane

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /bin/controlplane /bin/controlplane
EXPOSE 8080 9090
ENTRYPOINT ["/bin/controlplane"]
