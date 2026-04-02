package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	// Server
	ServerAddr   string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration

	// ClickHouse
	ClickHouseAddr     string
	ClickHouseDatabase string
	ClickHouseUsername string
	ClickHousePassword string
	MaxOpenConns       int
	MaxIdleConns       int
	DialTimeout        time.Duration
	ConnMaxLifetime    time.Duration

	// Batch processing
	BatchSize     int
	FlushInterval time.Duration
	WorkerCount   int
	QueueSize     int

	// Retry
	MaxRetries    int
	RetryInterval time.Duration

	// Rate limiting
	RateLimitPerSec int
	RateLimitBurst  int

	// Cluster
	ClusterName string
	IngestorID  string

	// Security
	APIKey            string
	LogMasking        bool
	AllowedNamespaces []string

	// Tracing (OpenTelemetry)
	OtelEnabled     bool
	OtelEndpoint    string
	OtelServiceName string

	// Kafka (optional buffer)
	KafkaEnabled bool
	KafkaBrokers string
	KafkaTopic   string
	KafkaGroupID string

	// Redis (optional buffer)
	RedisEnabled  bool
	RedisAddr     string
	RedisPassword string
	RedisDB       int

	// Dead Letter Queue
	DLQEnabled    bool
	DLQMaxRetries int
}

func Load() *Config {
	c := &Config{
		ServerAddr:   getEnv("SERVER_ADDR", ":8080"),
		ReadTimeout:  time.Duration(getEnvInt("SERVER_READ_TIMEOUT", 30)) * time.Second,
		WriteTimeout: time.Duration(getEnvInt("SERVER_WRITE_TIMEOUT", 30)) * time.Second,

		ClickHouseAddr:     getEnv("CLICKHOUSE_ADDR", "127.0.0.1:9000"),
		ClickHouseDatabase: getEnv("CLICKHOUSE_DATABASE", "default"),
		ClickHouseUsername: getEnv("CLICKHOUSE_USERNAME", "admin"),
		ClickHousePassword: getEnv("CLICKHOUSE_PASSWORD", "admin123"),
		MaxOpenConns:       getEnvInt("CLICKHOUSE_MAX_OPEN_CONNS", 20),
		MaxIdleConns:       getEnvInt("CLICKHOUSE_MAX_IDLE_CONNS", 10),
		DialTimeout:        time.Duration(getEnvInt("CLICKHOUSE_DIAL_TIMEOUT", 5)) * time.Second,
		ConnMaxLifetime:    time.Duration(getEnvInt("CLICKHOUSE_CONN_MAX_LIFETIME", 3600)) * time.Second,

		BatchSize:     getEnvInt("BATCH_SIZE", 200),
		FlushInterval: time.Duration(getEnvInt("FLUSH_INTERVAL_SEC", 1)) * time.Second,
		WorkerCount:   getEnvInt("WORKER_COUNT", 4),
		QueueSize:     getEnvInt("QUEUE_SIZE", 50000),

		MaxRetries:    getEnvInt("MAX_RETRIES", 3),
		RetryInterval: time.Duration(getEnvInt("RETRY_INTERVAL_SEC", 1)) * time.Second,

		RateLimitPerSec: getEnvInt("RATE_LIMIT_PER_SEC", 1000),
		RateLimitBurst:  getEnvInt("RATE_LIMIT_BURST", 2000),

		ClusterName: getEnv("CLUSTER_NAME", "prod-cluster"),
		IngestorID:  getEnv("INGESTOR_ID", fmt.Sprintf("ingestor-%d", os.Getpid())),

		APIKey:     os.Getenv("API_KEY"),
		LogMasking: getEnvBool("LOG_MASKING_ENABLED", false),

		OtelEnabled:     getEnvBool("OTEL_ENABLED", false),
		OtelEndpoint:    getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),
		OtelServiceName: getEnv("OTEL_SERVICE_NAME", "k8s-ingestor"),

		KafkaEnabled: getEnvBool("KAFKA_ENABLED", false),
		KafkaBrokers: getEnv("KAFKA_BROKERS", "localhost:9092"),
		KafkaTopic:   getEnv("KAFKA_TOPIC", "k8s-logs"),
		KafkaGroupID: getEnv("KAFKA_GROUP_ID", "k8s-ingestor"),

		RedisEnabled:  getEnvBool("REDIS_ENABLED", false),
		RedisAddr:     getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),
		RedisDB:       getEnvInt("REDIS_DB", 0),

		DLQEnabled:    getEnvBool("DLQ_ENABLED", true),
		DLQMaxRetries: getEnvInt("DLQ_MAX_RETRIES", 5),
	}

	// Parse allowed namespaces
	namespaces := getEnv("ALLOWED_NAMESPACES", "")
	if namespaces != "" {
		c.AllowedNamespaces = parseNamespaces(namespaces)
	}

	return c
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		if boolVal, err := strconv.ParseBool(value); err == nil {
			return boolVal
		}
	}
	return defaultValue
}

func parseNamespaces(s string) []string {
	var namespaces []string
	for _, ns := range splitAndTrim(s, ",") {
		if ns != "" {
			namespaces = append(namespaces, ns)
		}
	}
	return namespaces
}

func splitAndTrim(s, sep string) []string {
	var result []string
	for _, part := range split(s, sep) {
		result = append(result, trim(part))
	}
	return result
}

func split(s, sep string) []string {
	if s == "" {
		return nil
	}
	var result []string
	start := 0
	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			result = append(result, s[start:i])
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	result = append(result, s[start:])
	return result
}

func trim(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
