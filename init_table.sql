CREATE TABLE logs (
  timestamp DateTime,
  cluster String,
  namespace String,
  pod String,
  container String,
  level LowCardinality(String),
  message String,
  labels Map(String, String)
)
ENGINE = MergeTree()
PARTITION BY toDate(timestamp)
ORDER BY (timestamp, namespace, pod);