FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod ./
RUN go mod download || true

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o worker-service .

FROM scratch
COPY --from=builder /app/worker-service /worker-service
ENV POD_NAMESPACE=demo
ENTRYPOINT ["/worker-service"]
