FROM golang:1.25-alpine AS builder

ENV GOTOOLCHAIN=auto

ARG VERSION=dev
ARG GIT_COMMIT=unknown

WORKDIR /build

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w -X main.version=${VERSION} -X main.commit=${GIT_COMMIT}" \
    -o k8s-ingestor .

FROM scratch

LABEL org.opencontainers.image.title="k8s-log-ingestor"
LABEL org.opencontainers.image.description="Kubernetes log ingestion with Fluent Bit and ClickHouse"
LABEL org.opencontainers.image.version="${VERSION:-dev}"
LABEL org.opencontainers.image.source="https://github.com/example/k8s-ingestor"
LABEL org.opencontainers.image.licenses="MIT"

COPY --from=builder /build/k8s-ingestor /app/k8s-ingestor
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd

USER 1000:1000

WORKDIR /app

EXPOSE 8080 9090

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1

ENTRYPOINT ["/app/k8s-ingestor"]
