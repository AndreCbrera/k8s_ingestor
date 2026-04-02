#!/bin/bash
set -euo pipefail

NAMESPACE="${NAMESPACE:-logging}"
CERT_DIR="${CERT_DIR:-/tmp/certs}"
DAYS="${DAYS:-365}"

echo "[*] Generating mTLS certificates for k8s-ingestor..."

mkdir -p "${CERT_DIR}"

generate_ca() {
  echo "[*] Generating CA certificate..."
  openssl genrsa -out "${CERT_DIR}/ca.key" 4096 2>/dev/null
  openssl req -x509 -new -nodes -key "${CERT_DIR}/ca.key -sha256" \
    -days "${DAYS}" -out "${CERT_DIR}/ca.crt" \
    -subj "/CN=k8s-ingestor-ca" 2>/dev/null
}

generate_server_cert() {
  echo "[*] Generating server certificate..."
  openssl genrsa -out "${CERT_DIR}/server.key" 2048 2>/dev/null
  
  cat > "${CERT_DIR}/server.cnf" <<EOF
[req]
default_bits = 2048
prompt = no
default_md = sha256
distinguished_name = dn
x509_extensions = v3_req

[dn]
C = US
ST = State
L = City
O = Kubernetes
OU = Logging
CN = k8s-ingestor.logging.svc.cluster.local

[v3_req]
basicConstraints = CA:FALSE
keyUsage = nonRepudiation, digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth, clientAuth
subjectAltName = @alt_names

[alt_names]
DNS.1 = k8s-ingestor
DNS.2 = k8s-ingestor.logging
DNS.3 = k8s-ingestor.logging.svc
DNS.4 = k8s-ingestor.logging.svc.cluster.local
IP.1 = 127.0.0.1
EOF

  openssl req -new -key "${CERT_DIR}/server.key" \
    -out "${CERT_DIR}/server.csr" \
    -config "${CERT_DIR}/server.cnf" 2>/dev/null
    
  openssl x509 -req -in "${CERT_DIR}/server.csr" \
    -CA "${CERT_DIR}/ca.crt" -CAkey "${CERT_DIR}/ca.key" \
    -CAcreateserial -out "${CERT_DIR}/server.crt" \
    -days "${DAYS}" -extensions v3_req \
    -extfile "${CERT_DIR}/server.cnf" 2>/dev/null
    
  rm -f "${CERT_DIR}/server.csr" "${CERT_DIR}/server.cnf"
}

generate_client_cert() {
  echo "[*] Generating client certificate..."
  openssl genrsa -out "${CERT_DIR}/client.key" 2048 2>/dev/null
  
  cat > "${CERT_DIR}/client.cnf" <<EOF
[req]
default_bits = 2048
prompt = no
default_md = sha256
distinguished_name = dn

[dn]
C = US
ST = State
L = City
O = Kubernetes
OU = Logging
CN = fluent-bit
EOF

  openssl req -new -key "${CERT_DIR}/client.key" \
    -out "${CERT_DIR}/client.csr" \
    -config "${CERT_DIR}/client.cnf" 2>/dev/null
    
  openssl x509 -req -in "${CERT_DIR}/client.csr" \
    -CA "${CERT_DIR}/ca.crt" -CAkey "${CERT_DIR}/ca.key" \
    -CAcreateserial -out "${CERT_DIR}/client.crt" \
    -days "${DAYS}" 2>/dev/null
    
  rm -f "${CERT_DIR}/client.csr" "${CERT_DIR}/client.cnf"
}

generate_ca
generate_server_cert
generate_client_cert

echo "[*] Creating Kubernetes secrets..."
kubectl create secret generic mtls-ca \
  --from-file=ca.crt="${CERT_DIR}/ca.crt" \
  --namespace="${NAMESPACE}" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret tls mtls-server \
  --cert="${CERT_DIR}/server.crt" \
  --key="${CERT_DIR}/server.key" \
  --namespace="${NAMESPACE}" \
  --dry-run=client -o yaml | kubectl apply -f -

kubectl create secret generic mtls-client \
  --from-file=client.crt="${CERT_DIR}/client.crt" \
  --from-file=client.key="${CERT_DIR}/client.key" \
  --namespace="${NAMESPACE}" \
  --dry-run=client -o yaml | kubectl apply -f -

echo "[*] Certificates generated and applied to namespace ${NAMESPACE}"
echo ""
echo "Files created:"
ls -la "${CERT_DIR}"
