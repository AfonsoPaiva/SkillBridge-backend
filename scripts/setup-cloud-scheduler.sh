#!/bin/bash
# =============================================================================
# setup-cloud-scheduler.sh
# Configura um Cloud Scheduler job para triggar o scrape do LinkedIn
# periodicamente no Cloud Run do SkillBridge Backend.
#
# Pré-requisitos:
#   - gcloud CLI autenticado com permissões suficientes
#   - Cloud Scheduler API ativada no projeto
#   - Um token de admin Firebase válido para autenticar (ou usar OIDC)
#
# Uso:
#   chmod +x setup-cloud-scheduler.sh
#   ./setup-cloud-scheduler.sh
# =============================================================================

set -e

# ── Configuração ──────────────────────────────────────────────────────────────
PROJECT_ID="${GCS_PROJECT_ID:-$(gcloud config get-value project)}"
REGION="europe-west1"
BACKEND_URL="https://skillbridge-backend-<HASH>-ew.a.run.app"  # <- Substitui pelo URL real do Cloud Run
SERVICE_ACCOUNT="skillbridge-scheduler@${PROJECT_ID}.iam.gserviceaccount.com"

# ── Criar Service Account para o Scheduler (se não existir) ──────────────────
echo "🔧 A criar service account para o Cloud Scheduler..."
gcloud iam service-accounts create skillbridge-scheduler \
  --display-name="SkillBridge Cloud Scheduler" \
  --project="${PROJECT_ID}" 2>/dev/null || echo "  (já existe)"

# Permitir que a service account invoque o Cloud Run
gcloud run services add-iam-policy-binding skillbridge-backend \
  --region="${REGION}" \
  --member="serviceAccount:${SERVICE_ACCOUNT}" \
  --role="roles/run.invoker" \
  --project="${PROJECT_ID}"

echo "✓ Permissão run.invoker concedida"

# ── Criar job do Cloud Scheduler ─────────────────────────────────────────────
# Corre a cada 28 dias (às 03:00 UTC) para fazer scrape do LinkedIn
echo "🕒 A criar Cloud Scheduler job (a cada 28 dias)..."

gcloud scheduler jobs create http skillbridge-linkedin-scrape \
  --location="${REGION}" \
  --schedule="0 3 1,29 * *" \
  --uri="${BACKEND_URL}/api/admin/scrape-jobs" \
  --http-method=POST \
  --oidc-service-account-email="${SERVICE_ACCOUNT}" \
  --oidc-token-audience="${BACKEND_URL}" \
  --attempt-deadline=30m \
  --description="Scrape LinkedIn jobs periodicamente para o SkillBridge" \
  --project="${PROJECT_ID}" 2>/dev/null || \
gcloud scheduler jobs update http skillbridge-linkedin-scrape \
  --location="${REGION}" \
  --schedule="0 3 1,29 * *" \
  --uri="${BACKEND_URL}/api/admin/scrape-jobs" \
  --http-method=POST \
  --oidc-service-account-email="${SERVICE_ACCOUNT}" \
  --oidc-token-audience="${BACKEND_URL}" \
  --attempt-deadline=30m \
  --project="${PROJECT_ID}"

echo ""
echo "✅ Cloud Scheduler configurado com sucesso!"
echo ""
echo "   Job: skillbridge-linkedin-scrape"
echo "   Schedule: 0 3 1,29 * * (dias 1 e 29 de cada mês, às 03:00 UTC)"
echo "   Endpoint: POST ${BACKEND_URL}/api/admin/scrape-jobs"
echo ""
echo "Para triggar manualmente:"
echo "   gcloud scheduler jobs run skillbridge-linkedin-scrape --location=${REGION}"
