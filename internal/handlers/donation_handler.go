package handlers

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/checkout/session"
	"github.com/stripe/stripe-go/v76/webhook"
)

// ---------------------------------------------------------------------------
// IP-based rate limiting for the unauthenticated donation checkout endpoint.
// Limiting to 5 checkout sessions per hour per IP prevents Stripe API abuse
// without requiring donors to have an account.
// ---------------------------------------------------------------------------

var (
	donationRateMux  sync.Mutex
	donationAttempts = make(map[string][]time.Time)
)

const (
	donationRateMax    = 5
	donationRateWindow = time.Hour
)

// checkDonationRateLimit returns true when the IP is within the allowed quota.
// It also evicts entries older than the window to bound memory growth.
func checkDonationRateLimit(ip string) bool {
	donationRateMux.Lock()
	defer donationRateMux.Unlock()

	now := time.Now()
	cutoff := now.Add(-donationRateWindow)

	prior := donationAttempts[ip]
	valid := prior[:0]
	for _, t := range prior {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= donationRateMax {
		return false
	}

	donationAttempts[ip] = append(valid, now)
	return true
}

func stripeReady(c *gin.Context) bool {
	if config.AppConfig.StripeSecretKey == "" {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Stripe não configurado no servidor."})
		return false
	}
	return true
}

// CreateEmbeddedCheckoutSession - Cria uma sessão Stripe Embedded Checkout para donativos.
//
// @Summary      Criar Embedded Checkout Session
// @Description  Cria uma Checkout Session em modo embedded e devolve o client_secret para montar no frontend
// @Tags         donations
// @Accept       json
// @Produce      json
// @Param        body  body      object  true  "Donation details"  example({"amount": 1000})
// @Success      200   {object}  map[string]string
// @Failure      400   {object}  map[string]string
// @Failure      503   {object}  map[string]string
// @Router       /donations/embedded-checkout [post]
func CreateEmbeddedCheckoutSession(c *gin.Context) {
	if !stripeReady(c) {
		return
	}

	// Enforce per-IP rate limit before touching the Stripe API.
	// c.ClientIP() resolves X-Forwarded-For / X-Real-IP automatically (Gin trusts
	// the proxy headers set by Cloud Run's load balancer).
	if !checkDonationRateLimit(c.ClientIP()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Demasiados pedidos. Tente novamente mais tarde."})
		return
	}

	var req struct {
		Amount int64 `json:"amount" binding:"required,min=100,max=1000000"` // cents, min €1, max €10000
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Montante inválido (mínimo €1, máximo €10.000)."})
		return
	}

	stripe.Key = config.AppConfig.StripeSecretKey

	returnURL := config.AppConfig.FrontendURL + "/donation"

	params := &stripe.CheckoutSessionParams{
		Mode:      stripe.String(string(stripe.CheckoutSessionModePayment)),
		UIMode:    stripe.String("embedded"),
		ReturnURL: stripe.String(returnURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Quantity: stripe.Int64(1),
				PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
					Currency: stripe.String(string(stripe.CurrencyEUR)),
					ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
						Name: stripe.String("Donativo SkillBridge"),
					},
					UnitAmount: stripe.Int64(req.Amount),
				},
			},
		},
	}
	params.AddMetadata("type", "donation")

	sess, err := session.New(params)
	if err != nil {
		log.Printf("Erro ao criar Embedded Checkout Session: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Não foi possível criar checkout."})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"clientSecret": sess.ClientSecret,
	})
}

// StripeWebhook - Recebe eventos do Stripe para atualizar o estado dos donativos
//
// @Summary      Webhook Stripe
// @Description  Recebe eventos do Stripe quando um pagamento via Payment Link é completado
// @Tags         donations
// @Accept       json
// @Produce      json
// @Param        Stripe-Signature  header  string  true  "Assinatura do Stripe"
// @Success      200  {object}  map[string]bool
// @Failure      400  {object}  map[string]string
// @Router       /donations/webhook [post]
func StripeWebhook(c *gin.Context) {
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Erro ao ler payload."})
		return
	}

	// Verificar assinatura do Stripe para segurança
	sigHeader := c.GetHeader("Stripe-Signature")
	event, err := webhook.ConstructEvent(payload, sigHeader, config.AppConfig.StripeWebhookSecret)
	if err != nil {
		log.Printf("Erro na verificação do webhook Stripe: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Assinatura inválida."})
		return
	}

	// Processar o evento
	switch event.Type {

	case "checkout.session.completed":
		var session stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
			log.Printf("Erro ao fazer parse do evento: %v", err)
			break
		}

		// Não guardar dados de pagamento localmente.
		// O Stripe é a fonte de verdade para pagamentos.
		log.Printf("✅ Donativo confirmado no Stripe: session=%s amount=%.2f€ currency=%s",
			session.ID,
			float64(session.AmountTotal)/100,
			session.Currency)
	}

	c.JSON(http.StatusOK, gin.H{"received": true})
}

// GetDonationStats - Estatísticas públicas de donativos
//
// @Summary      Estatísticas de donativos
// @Description  Devolve o total e contagem de donativos confirmados consultando o Stripe (sem persistência local)
// @Tags         donations
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      503  {object}  map[string]string
// @Router       /donations/stats [get]
func GetDonationStats(c *gin.Context) {
	if !stripeReady(c) {
		return
	}

	stripe.Key = config.AppConfig.StripeSecretKey

	params := &stripe.CheckoutSessionListParams{}
	params.Limit = stripe.Int64(100)

	iter := session.List(params)

	var totalAmount int64
	var count int64

	for iter.Next() {
		s := iter.CheckoutSession()
		if s.PaymentStatus == stripe.CheckoutSessionPaymentStatusPaid {
			totalAmount += s.AmountTotal
			count++
		}
	}

	if err := iter.Err(); err != nil {
		log.Printf("Erro ao obter estatísticas de donativos no Stripe: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Não foi possível obter estatísticas no Stripe."})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"total_euros": float64(totalAmount) / 100,
		"total_count": count,
		"currency":    "eur",
	})
}
