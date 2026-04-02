#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
KIND_NAME="${KIND_NAME:-kind}"

echo "=== Building and deploying sample apps ==="

cd "$ROOT_DIR/sample-apps"

echo "--- Building Docker images ---"
docker build -t demo-api:latest ./api
docker build -t demo-web:latest ./web
docker build -t demo-worker:latest ./worker

echo "--- Loading images into kind ---"
kind load docker-image demo-api:latest --name "$KIND_NAME"
kind load docker-image demo-web:latest --name "$KIND_NAME"
kind load docker-image demo-worker:latest --name "$KIND_NAME"

echo "--- Creating demo namespace ---"
kubectl create namespace demo --dry-run=client -o yaml | kubectl apply -f -

echo "--- Deploying sample apps ---"
cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api-service
  namespace: demo
spec:
  replicas: 2
  selector:
    matchLabels:
      app: api-service
  template:
    metadata:
      labels:
        app: api-service
        app.kubernetes.io/name: api-service
    spec:
      containers:
      - name: api-service
        image: demo-api:latest
        imagePullPolicy: Never
        env:
        - name: POD_NAMESPACE
          value: demo
        - name: PORT
          value: "8080"
        ports:
        - containerPort: 8080
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: web-frontend
  namespace: demo
spec:
  replicas: 2
  selector:
    matchLabels:
      app: web-frontend
  template:
    metadata:
      labels:
        app: web-frontend
        app.kubernetes.io/name: web-frontend
    spec:
      containers:
      - name: web-frontend
        image: demo-web:latest
        imagePullPolicy: Never
        env:
        - name: POD_NAMESPACE
          value: demo
        - name: PORT
          value: "8081"
        ports:
        - containerPort: 8081
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: worker-service
  namespace: demo
spec:
  replicas: 2
  selector:
    matchLabels:
      app: worker-service
  template:
    metadata:
      labels:
        app: worker-service
        app.kubernetes.io/name: worker-service
    spec:
      containers:
      - name: worker-service
        image: demo-worker:latest
        imagePullPolicy: Never
        env:
        - name: POD_NAMESPACE
          value: demo
EOF

echo "--- Creating services ---"
cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Service
metadata:
  name: api-service
  namespace: demo
spec:
  selector:
    app: api-service
  ports:
  - port: 8080
    targetPort: 8080
---
apiVersion: v1
kind: Service
metadata:
  name: web-frontend
  namespace: demo
spec:
  selector:
    app: web-frontend
  ports:
  - port: 8081
    targetPort: 8081
EOF

echo "=== Sample apps deployed ==="
echo ""
echo "Pods in demo namespace:"
kubectl get pods -n demo
