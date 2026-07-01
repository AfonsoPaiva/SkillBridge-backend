package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/email"
	"github.com/paiva/SkillBridge/Backend/internal/models"
	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/checkout/session"
)

// ---------------------------------------------------------------------------
// IP + user-based rate limiting for the contest registration endpoint.
// 3 checkout attempts per hour per IP prevents Stripe API abuse.
// ---------------------------------------------------------------------------

var (
	contestRateMux  sync.Mutex
	contestAttempts = make(map[string][]time.Time)
)

const (
	contestRateMax    = 3
	contestRateWindow = time.Hour
	// Fixed registration fee in cents (€7.00)
	contestFeeCents = 700
)

// validContestTracks lists all accepted track codes from the Build Challenge regulation.
var validContestTracks = map[string]bool{
	"A": true, "B": true, "C": true, "D": true,
	"E": true, "F": true, "G": true, "H": true,
	"I": true, "J": true, "K": true, "L": true,
}

func checkContestRateLimit(ip string) bool {
	contestRateMux.Lock()
	defer contestRateMux.Unlock()

	now := time.Now()
	cutoff := now.Add(-contestRateWindow)

	prior := contestAttempts[ip]
	valid := prior[:0]
	for _, t := range prior {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= contestRateMax {
		return false
	}

	contestAttempts[ip] = append(valid, now)
	return true
}

// ContestRegister creates a Stripe checkout session for the Build Challenge
// registration fee (€7.00 fixed). The user must be authenticated, must own
// or be a member of the selected project, and cannot register twice.
//
// @Summary      Inscrição no Build Challenge
// @Description  Cria sessão Stripe para pagamento da inscrição (7 €)
// @Tags         contest
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body  object  true  "Registration details" example({"project_id": 1, "track": "A"})
// @Success      200   {object}  map[string]string
// @Failure      400   {object}  map[string]string
// @Failure      409   {object}  map[string]string
// @Failure      429   {object}  map[string]string
// @Router       /contest/register [post]
func ContestRegister(c *gin.Context) {
	if !stripeReady(c) {
		return
	}

	// Rate limit
	if !checkContestRateLimit(c.ClientIP()) {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Demasiados pedidos. Tente novamente mais tarde."})
		return
	}

	uid, exists := c.Get("firebase_uid")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Autenticação obrigatória."})
		return
	}

	// Resolve user from Firebase UID
	var user models.User
	if err := database.DB.Where("firebase_uid = ?", uid).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var req struct {
		ProjectID uint   `json:"project_id" binding:"required"`
		Track     string `json:"track" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados inválidos. Selecione um projeto e uma trilha."})
		return
	}

	// Validate track
	track := strings.ToUpper(strings.TrimSpace(req.Track))
	if !validContestTracks[track] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Trilha inválida. Escolha entre A e L."})
		return
	}

	// Verify the project exists
	var project models.Project
	if err := database.DB.Where("id = ?", req.ProjectID).First(&project).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Projeto não encontrado."})
		return
	}

	// Verify user is the owner of the project
	if project.OwnerID != user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Apenas o dono do projeto pode inscrever a equipa no Build Challenge."})
		return
	}

	// Check if this project is already registered (prevent double registration)
	var existingReg models.ContestRegistration
	if err := database.DB.Where("project_id = ?", req.ProjectID).First(&existingReg).Error; err == nil {
		if existingReg.PaymentStatus == "paid" {
			c.JSON(http.StatusConflict, gin.H{"error": "Este projeto (e a respetiva equipa) já se encontra inscrito no Build Challenge."})
			return
		}
		// If pending, allow retry — delete old one
		database.DB.Delete(&existingReg)
	}

	// Optional: Check if the user (owner) is already registered via another project
	var otherReg models.ContestRegistration
	if err := database.DB.Where("user_id = ? AND payment_status = 'paid'", user.ID).First(&otherReg).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Já te encontras inscrito noutro projeto."})
		return
	}

	// Create Stripe checkout session
	// The team size is calculated dynamically (owner + accepted members).
	var acceptedMembers int64
	database.DB.Model(&models.ProjectMember{}).
		Where("project_id = ? AND status = 'accepted'", req.ProjectID).
		Count(&acceptedMembers)
	totalTeamSize := acceptedMembers + 1 // +1 for the owner

	stripe.Key = config.AppConfig.StripeSecretKey

	returnURL := config.AppConfig.FrontendURL + "/contest?payment=complete"

	params := &stripe.CheckoutSessionParams{
		Mode:      stripe.String(string(stripe.CheckoutSessionModePayment)),
		UIMode:    stripe.String("embedded"),
		ReturnURL: stripe.String(returnURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Quantity: stripe.Int64(totalTeamSize),
				PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
					Currency: stripe.String(string(stripe.CurrencyEUR)),
					ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
						Name:        stripe.String("Build Challenge — Inscrição de Equipa"),
						Description: stripe.String(fmt.Sprintf("Taxa de inscrição para %d elementos", totalTeamSize)),
					},
					UnitAmount: stripe.Int64(contestFeeCents),
				},
			},
		},
	}
	params.AddMetadata("type", "contest")
	params.AddMetadata("user_id", fmt.Sprintf("%d", user.ID))
	params.AddMetadata("project_id", fmt.Sprintf("%d", req.ProjectID))
	params.AddMetadata("track", track)

	sess, err := session.New(params)
	if err != nil {
		log.Printf("Erro ao criar Checkout Session para concurso: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Não foi possível criar checkout."})
		return
	}

	// Save registration with pending status
	reg := models.ContestRegistration{
		UserID:          user.ID,
		ProjectID:       req.ProjectID,
		Track:           track,
		StripeSessionID: sess.ID,
		PaymentStatus:   "pending",
	}
	if err := database.DB.Create(&reg).Error; err != nil {
		log.Printf("Erro ao guardar inscrição no concurso: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao guardar inscrição."})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"clientSecret": sess.ClientSecret,
	})
}

// ContestGetMyRegistration returns the authenticated user's contest registration
// status, including payment status and project info.
//
// @Summary      Estado da inscrição
// @Tags         contest
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  map[string]string
// @Router       /contest/registrations/me [get]
func ContestGetMyRegistration(c *gin.Context) {
	uid, exists := c.Get("firebase_uid")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Autenticação obrigatória."})
		return
	}

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", uid).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var reg models.ContestRegistration
	err := database.DB.Preload("Project").Where("user_id = ?", user.ID).First(&reg).Error
	
	if err != nil {
		// Se não foi o próprio a inscrever (dono), verifica se é membro de um projeto inscrito
		var memberProjects []uint
		database.DB.Model(&models.ProjectMember{}).
			Where("user_id = ? AND status = 'accepted'", user.ID).
			Pluck("project_id", &memberProjects)
		
		if len(memberProjects) > 0 {
			err = database.DB.Preload("Project").Where("project_id IN ?", memberProjects).First(&reg).Error
		}
	}

	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Não estás inscrito no Build Challenge."})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"registration": reg,
	})
}

// ContestGetStats returns public statistics about contest registrations,
// including total registrations per track and overall totals.
//
// @Summary      Estatísticas do concurso
// @Tags         contest
// @Success      200  {object}  map[string]interface{}
// @Router       /contest/stats [get]
func ContestGetStats(c *gin.Context) {
	type TrackStat struct {
		Track string `json:"track"`
		Count int64  `json:"count"`
	}

	var stats []TrackStat
	database.DB.Model(&models.ContestRegistration{}).
		Select("track, COUNT(*) as count").
		Where("payment_status = 'paid'").
		Group("track").
		Scan(&stats)

	var totalPaid int64
	database.DB.Model(&models.ContestRegistration{}).
		Where("payment_status = 'paid'").
		Count(&totalPaid)

	c.JSON(http.StatusOK, gin.H{
		"tracks":     stats,
		"total_paid": totalPaid,
	})
}

// ContestGetUserProjects returns projects owned by the authenticated user,
// for contest registration project selection. Only owners can register.
//
// @Summary      Projetos do utilizador para inscrição
// @Tags         contest
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Router       /contest/my-projects [get]
func ContestGetUserProjects(c *gin.Context) {
	uid, exists := c.Get("firebase_uid")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Autenticação obrigatória."})
		return
	}

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", uid).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	// Only return projects owned by the user
	var projects []models.Project
	database.DB.Preload("Roles").Preload("Members", "status = 'accepted'").
		Where("owner_id = ?", user.ID).Find(&projects)

	c.JSON(http.StatusOK, gin.H{
		"projects": projects,
	})
}

// handleContestWebhook processes contest-specific webhook events.
// Called from the main StripeWebhook handler when metadata.type == "contest".
func HandleContestWebhook(sess *stripe.CheckoutSession) {
	if sess.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		log.Printf("⚠️ Tentativa de confirmar concurso com payment_status = %s (esperado 'paid')", sess.PaymentStatus)
		return
	}

	// Update registration status
	result := database.DB.Model(&models.ContestRegistration{}).
		Where("stripe_session_id = ?", sess.ID).
		Update("payment_status", "paid")

	if result.RowsAffected > 0 {
		log.Printf("✅ Inscrição Build Challenge confirmada: session=%s user=%s project=%s track=%s",
			sess.ID,
			sess.Metadata["user_id"],
			sess.Metadata["project_id"],
			sess.Metadata["track"])

		// Enviar emails de confirmação
		var project models.Project
		if err := database.DB.Preload("Owner").Preload("Members", "status = 'accepted'").Preload("Members.User").
			Where("id = ?", sess.Metadata["project_id"]).First(&project).Error; err == nil {
			
			// Enviar para o dono
			if project.Owner.Email != "" {
				go email.SendContestRegistrationConfirmed(project.Owner.Email, project.Owner.Name, project.Title)
			}
			
			// Enviar para os membros aceites
			for _, m := range project.Members {
				if m.User.Email != "" {
					go email.SendContestRegistrationConfirmed(m.User.Email, m.User.Name, project.Title)
				}
			}
		} else {
			log.Printf("Erro ao carregar projeto para enviar emails de concurso: %v", err)
		}
	}
}
