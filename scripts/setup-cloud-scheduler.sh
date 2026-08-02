#!/bin/bash
# =============================================================================
# setup-cloud-scheduler.sh
# Configura um Cloud Scheduler job para triggar o scrape de emprego
# periodicamente no Cloud Run do SkillBridge Backend.
#
# Autenticação: header  X-Scrape-Secret  (não requer Firebase nem OIDC)
# O segredo deve ser definido como variável de ambiente SCRAPE_SECRET
# no Cloud Run E passado aqui como variável SCRAPE_SECRET.
#
# Pré-requisitos:
#   - gcloud CLI autenticado com permissões suficientes
#   - Cloud Scheduler API ativada no projeto
#   - SCRAPE_SECRET definido (mesmo valor que está no Cloud Run)
#
# Uso:
#   export SCRAPE_SECRET="o-teu-segredo-longo-aqui"
#   chmod +x setup-cloud-scheduler.sh
#   ./setup-cloud-scheduler.sh
# =============================================================================

set -e

# ── Configuração ──────────────────────────────────────────────────────────────
PROJECT_ID="${GCS_PROJECT_ID:-$(gcloud config get-value project)}"
REGION="europe-west1"
BACKEND_URL="https://backendskillbridge-742354947031.europe-west1.run.app"
JOB_NAME="Search-new-jobs"

if [ -z "${SCRAPE_SECRET}" ]; then
  echo "❌ SCRAPE_SECRET não está definido."
  echo "   Executa:  export SCRAPE_SECRET=\"o-teu-segredo\""
  exit 1
fi

ENDPOINT="${BACKEND_URL}/api/internal/scrape-jobs"

echo "🔧 A configurar Cloud Scheduler job '${JOB_NAME}'..."
echo "   Endpoint: POST ${ENDPOINT}"
echo "   Project:  ${PROJECT_ID}"
echo "   Region:   ${REGION}"
echo ""

# ── Criar ou actualizar o job ─────────────────────────────────────────────────
# Tenta criar; se já existir, faz update.
gcloud scheduler jobs create http "${JOB_NAME}" \
  --location="${REGION}" \
  --schedule="0 3 1,29 * *" \
  --uri="${ENDPOINT}" \
  --http-method=POST \
  --headers="X-Scrape-Secret=${SCRAPE_SECRET},Content-Type=application/json" \
  --message-body="{}" \
  --attempt-deadline=30m \
  --description="Scrape multi-plataforma de vagas junior/estágio para o SkillBridge" \
  --project="${PROJECT_ID}" 2>/dev/null \
|| \
gcloud scheduler jobs update http "${JOB_NAME}" \
  --location="${REGION}" \
  --schedule="0 3 1,29 * *" \
  --uri="${ENDPOINT}" \
  --http-method=POST \
  --headers="X-Scrape-Secret=${SCRAPE_SECRET},Content-Type=application/json" \
  --message-body="{}" \
  --attempt-deadline=30m \
  --project="${PROJECT_ID}"

echo ""
echo "✅ Cloud Scheduler configurado com sucesso!"
echo ""
echo "   Job:      ${JOB_NAME}"
echo "   Schedule: 0 3 1,29 * * (dias 1 e 29 de cada mês, às 03:00 UTC)"
echo "   Endpoint: POST ${ENDPOINT}"
echo ""
echo "Para triggar manualmente:"
echo "   gcloud scheduler jobs run ${JOB_NAME} --location=${REGION}"
echo ""
echo "⚠️  Certifica-te que SCRAPE_SECRET está definido no Cloud Run:"
echo "   gcloud run services update backendskillbridge \\"
echo "     --region=${REGION} \\"
echo "     --set-env-vars=SCRAPE_SECRET=${SCRAPE_SECRET}"

