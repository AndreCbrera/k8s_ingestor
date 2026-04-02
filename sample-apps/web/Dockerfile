FROM golang:1.21-alpine AS builder

WORKDIR /app
COPY go.mod ./
RUN go mod download || true

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o web-frontend .

FROM scratch
COPY --from=builder /app/web-frontend /web-frontend
ENV POD_NAMESPACE=demo
ENV PORT=8081
ENTRYPOINT ["/web-frontend"]
