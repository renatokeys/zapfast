#!/usr/bin/env bash
# install.sh - one-shot bootstrap for the zapfast K3s cluster.
#
# Prerequisites:
#   - kubectl + helm v3 installed
#   - KUBECONFIG points to the target K3s cluster
#   - Cluster has hcloud-volumes StorageClass (Hetzner CSI)
#   - DNS for zapfast.<your-domain> already pointing to Traefik LB IP
#
# Usage:
#   export KUBECONFIG=~/.kube/zapfast.config
#   bash bootstrap/install.sh
#
# Idempotent: re-running is safe; helm install becomes helm upgrade.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Pinned versions
CERT_MANAGER_VERSION="v1.16.2"
CNPG_RELEASE_URL="https://raw.githubusercontent.com/cloudnative-pg/cloudnative-pg/release-1.24/releases/cnpg-1.24.1.yaml"
KUBE_PROM_VERSION="62.7.0"

log() { printf '\033[1;36m[bootstrap]\033[0m %s\n' "$*"; }
err() { printf '\033[1;31m[error]\033[0m %s\n' "$*" >&2; }

require() {
  command -v "$1" >/dev/null 2>&1 || { err "missing dependency: $1"; exit 1; }
}

require kubectl
require helm
require openssl

log "verifying cluster connectivity"
kubectl cluster-info >/dev/null

# ------------------------------------------------------------------
log "step 1/8 - applying namespaces"
kubectl apply -f "${SCRIPT_DIR}/01-namespaces.yaml"

# ------------------------------------------------------------------
log "step 2/8 - installing cert-manager (${CERT_MANAGER_VERSION})"
helm repo add jetstack https://charts.jetstack.io --force-update >/dev/null
helm repo update >/dev/null

helm upgrade --install cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --version "${CERT_MANAGER_VERSION}" \
  --set crds.enabled=true \
  --set global.leaderElection.namespace=cert-manager \
  --wait \
  --timeout 5m

log "  waiting for cert-manager webhook"
kubectl -n cert-manager rollout status deploy/cert-manager-webhook --timeout=3m

# ------------------------------------------------------------------
log "step 3/8 - installing CloudNativePG operator"
kubectl apply --server-side -f "${CNPG_RELEASE_URL}"

log "  waiting for CNPG operator to be ready"
kubectl -n cnpg-system rollout status deployment/cnpg-controller-manager --timeout=5m

# ------------------------------------------------------------------
log "step 4/8 - applying ClusterIssuers (letsencrypt prod + staging)"
kubectl apply -f "${SCRIPT_DIR}/05-letsencrypt-issuer.yaml"

# ------------------------------------------------------------------
log "step 5/8 - provisioning Postgres cluster (zapfast-pg)"

PG_SECRET_FILE="${SCRIPT_DIR}/04-postgres-cluster.yaml"

if grep -q "CHANGE_ME_BEFORE_APPLY" "${PG_SECRET_FILE}"; then
  err "04-postgres-cluster.yaml still contains 'CHANGE_ME_BEFORE_APPLY'."
  err "Generate a password (openssl rand -base64 32) and update the manifest,"
  err "or set the secret manually before re-running this script."
  exit 1
fi

kubectl apply -f "${PG_SECRET_FILE}"

log "  waiting for Postgres cluster to become healthy (this can take ~2min)"
for i in $(seq 1 60); do
  PHASE=$(kubectl -n zapfast get cluster.postgresql.cnpg.io zapfast-pg \
    -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
  if [[ "${PHASE}" == "Cluster in healthy state" ]]; then
    log "  Postgres ready (phase: ${PHASE})"
    break
  fi
  printf '.'
  sleep 5
done
echo

# ------------------------------------------------------------------
log "step 6/8 - installing kube-prometheus-stack (${KUBE_PROM_VERSION})"

GRAFANA_PASSWORD="$(openssl rand -base64 24)"

helm repo add prometheus-community https://prometheus-community.github.io/helm-charts --force-update >/dev/null
helm repo update >/dev/null

helm upgrade --install kube-prom prometheus-community/kube-prometheus-stack \
  --namespace monitoring \
  --version "${KUBE_PROM_VERSION}" \
  --set grafana.adminPassword="${GRAFANA_PASSWORD}" \
  --set prometheus.prometheusSpec.podMonitorSelectorNilUsesHelmValues=false \
  --set prometheus.prometheusSpec.serviceMonitorSelectorNilUsesHelmValues=false \
  --set prometheus.prometheusSpec.retention=15d \
  --set prometheus.prometheusSpec.storageSpec.volumeClaimTemplate.spec.resources.requests.storage=20Gi \
  --wait \
  --timeout 10m

# ------------------------------------------------------------------
log "step 7/8 - sanity check"
kubectl -n zapfast get all
kubectl -n cnpg-system get pods
kubectl -n cert-manager get pods
kubectl -n monitoring get pods

# ------------------------------------------------------------------
log "step 8/8 - credentials & access info"

cat <<EOF

==================================================================
 zapfast cluster bootstrap COMPLETE
==================================================================

Grafana credentials:
  Username: admin
  Password: ${GRAFANA_PASSWORD}

Port-forward Grafana locally:
  kubectl -n monitoring port-forward svc/kube-prom-grafana 3000:80
  open http://localhost:3000

Postgres connection (from inside cluster):
  Host: zapfast-pg-rw.zapfast.svc.cluster.local
  Port: 5432
  DB:   zapfast
  User: zapfast
  Pass: kubectl -n zapfast get secret zapfast-pg-app -o jsonpath='{.data.password}' | base64 -d

Next step - deploy zapfast itself:
  kubectl -n zapfast create secret generic zapfast-secret \\
    --from-literal=DB_USER=zapfast \\
    --from-literal=DB_PASSWORD="\$(kubectl -n zapfast get secret zapfast-pg-app -o jsonpath='{.data.password}' | base64 -d)" \\
    --from-literal=WUZAPI_ADMIN_TOKEN="\$(openssl rand -hex 32)"

  helm upgrade --install zapfast ../helm/zapfast \\
    -f ../helm/zapfast/values.yaml \\
    -f ../helm/zapfast/values-prod.yaml \\
    --namespace zapfast \\
    --set db.existingSecret=zapfast-secret

==================================================================
EOF
