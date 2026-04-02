#!/bin/bash
set -e

echo "=========================================="
echo "  K8s Log Ingestor - Deploy"
echo "=========================================="

NAMESPACE="logging"
CH_NAMESPACE="clickhouse-operator"

# 1. Create schema in ClickHouse
echo ""
echo "[1/5] Creating ClickHouse schema..."
kubectl exec -n "$CH_NAMESPACE" chi-clickhouse-cluster-cluster1-0-0 -- clickhouse-client --queries="
CREATE DATABASE IF NOT EXISTS logs_db;
CREATE TABLE IF NOT EXISTS logs_db.logs (
    timestamp DateTime64(3, 'UTC') DEFAULT now(),
    cluster String DEFAULT '',
    namespace String DEFAULT '',
    pod String DEFAULT '',
    container String DEFAULT '',
    level Enum8('error' = 1, 'warn' = 2, 'info' = 3, 'debug' = 4) DEFAULT 'info',
    message String DEFAULT '',
    labels Map(String, String) DEFAULT map(),
    annotations Map(String, String) DEFAULT map(),
    node_name String DEFAULT '',
    host_ip String DEFAULT '',
    pod_ip String DEFAULT '',
    trace_id String DEFAULT '',
    span_id String DEFAULT '',
    processed_at DateTime64(3, 'UTC') DEFAULT now(),
    ingestor_id String DEFAULT ''
) ENGINE = MergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (cluster, namespace, timestamp)
TTL timestamp + INTERVAL 30 DAY;
" 2>/dev/null || echo "Schema might already exist"

# 2. Create secret for ClickHouse password
echo ""
echo "[2/5] Creating secrets..."
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: clickhouse-credentials
  namespace: $NAMESPACE
type: Opaque
stringData:
  password: admin123
EOF

# 3. Deploy Fluent Bit
echo ""
echo "[3/5] Deploying Fluent Bit..."
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: ServiceAccount
metadata:
  name: fluent-bit
  namespace: $NAMESPACE
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: fluent-bit
rules:
- apiGroups: [""]
  resources:
  - namespaces
  - pods
  verbs: ["get", "list", "watch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: fluent-bit
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: fluent-bit
subjects:
- kind: ServiceAccount
  name: fluent-bit
  namespace: $NAMESPACE
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: fluent-bit-config
  namespace: $NAMESPACE
data:
  fluent-bit.conf: |
    [SERVICE]
        Flush         1
        Daemon        Off
        Log_Level     info
        Parsers_File  parsers.conf

    [INPUT]
        Name              tail
        Path              /var/log/containers/*.log
        Parser            cri
        Tag               kube.*
        Refresh_Interval  5
        Mem_Buf_Limit    50MB

    [INPUT]
        Name              systemd
        Tag               host.*
        Systemd_Filter    _SYSTEMD_UNIT=kubelet.service

    [FILTER]
        Name                kubernetes
        Match               kube.*
        Kube_URL            https://kubernetes.default.svc:443
        Kube_CA_File        /var/run/secrets/kubernetes.io/serviceaccount/ca.crt
        Kube_Token_File     /var/run/secrets/kubernetes.io/serviceaccount/token
        Kube_Tag_Prefix     kube.
        Merge_Log           On
        Keep_Log            Off
        K8S-Logging.Parser On
        K8S-Logging.Exclude On

    [FILTER]
        Name                lua
        Match               kube.*
        Script              /fluent-bit/etc/filter.lua
        Call                filter_by_namespace

    [OUTPUT]
        Name            http
        Match           kube.*
        Host            k8s-ingestor.logging.svc.cluster.local
        Port            8080
        Path            /logs
        Header          Content-Type application/json
        Format          json_lines
        Retry_Limit     3
        tls             off
EOF

# 4. Deploy Fluent Bit DaemonSet
echo ""
echo "[4/5] Deploying Fluent Bit DaemonSet..."
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: fluent-bit
  namespace: $NAMESPACE
spec:
  selector:
    matchLabels:
      app: fluent-bit
  template:
    metadata:
      labels:
        app: fluent-bit
    spec:
      serviceAccountName: fluent-bit
      containers:
      - name: fluent-bit
        image: cr.fluentbit.io/fluent/fluent-bit:3.0.3
        ports:
        - containerPort: 2020
        resources:
          requests:
            cpu: 10m
            memory: 50Mi
          limits:
            cpu: 100m
            memory: 100Mi
        volumeMounts:
        - name: varlog
          mountPath: /var/log
        - name: varlogcontainers
          mountPath: /var/log/containers
          readOnly: true
        - name: fluent-bit-config
          mountPath: /fluent-bit/etc
        - name: filter-script
          mountPath: /fluent-bit/etc/filter.lua
          subPath: filter.lua
      volumes:
      - name: varlog
        hostPath:
          path: /var/log
      - name: varlogcontainers
        hostPath:
          path: /var/log/containers
      - name: fluent-bit-config
        configMap:
          name: fluent-bit-config
      - name: filter-script
        configMap:
          name: fluent-bit-filter
EOF

# 5. Deploy K8s Ingestor
echo ""
echo "[5/5] Deploying K8s Ingestor..."
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: k8s-ingestor
  namespace: $NAMESPACE
spec:
  replicas: 2
  selector:
    matchLabels:
      app: k8s-ingestor
  template:
    metadata:
      labels:
        app: k8s-ingestor
    spec:
      containers:
      - name: ingestor
        image: k8s-ingestor:latest
        ports:
        - containerPort: 8080
        env:
        - name: SERVER_ADDR
          value: ":8080"
        - name: CLICKHOUSE_ADDR
          value: "clickhouse.clickhouse-operator.svc.cluster.local:9000"
        - name: CLICKHOUSE_DATABASE
          value: "default"
        - name: CLICKHOUSE_USERNAME
          value: "admin"
        - name: CLICKHOUSE_PASSWORD
          valueFrom:
            secretKeyRef:
              name: clickhouse-credentials
              key: password
        - name: CLUSTER_NAME
          value: "kind-cluster"
        - name: BATCH_SIZE
          value: "200"
        - name: WORKER_COUNT
          value: "4"
        - name: QUEUE_SIZE
          value: "50000"
        resources:
          requests:
            cpu: 100m
            memory: 128Mi
          limits:
            cpu: 500m
            memory: 512Mi
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
---
apiVersion: v1
kind: Service
metadata:
  name: k8s-ingestor
  namespace: $NAMESPACE
spec:
  ports:
  - port: 8080
    targetPort: 8080
  selector:
    app: k8s-ingestor
EOF

# Wait for deployment
echo ""
echo "Waiting for deployment..."
kubectl wait --for=condition=available --timeout=60s deployment/k8s-ingestor -n $NAMESPACE 2>/dev/null || true

# Show status
echo ""
echo "=========================================="
echo "  Deployment Status"
echo "=========================================="
echo ""
kubectl get pods -n $NAMESPACE
echo ""
kubectl get svc -n $NAMESPACE
echo ""
echo "=========================================="
echo "  Access"
echo "=========================================="
echo ""
echo "Port-forward to access:"
echo "  kubectl port-forward svc/k8s-ingestor 8080:8080 -n $NAMESPACE"
echo ""
echo "Then access:"
echo "  - Ingestor API:  http://localhost:8080"
echo "  - Health:        http://localhost:8080/health"
echo "  - Metrics:       http://localhost:8080/metrics"
echo ""
