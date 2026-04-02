FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o api-service .

FROM scratch
COPY --from=builder /app/api-service /api-service
ENV POD_NAMESPACE=demo
ENV PORT=8080
ENTRYPOINT ["/api-service"]
