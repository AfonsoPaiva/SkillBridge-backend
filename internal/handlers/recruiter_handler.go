package handlers

import (
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/email"
	"github.com/paiva/SkillBridge/Backend/internal/middleware"
	"github.com/paiva/SkillBridge/Backend/internal/models"
)

// blockedEmailDomains contains personal email domains that are not allowed for recruiter sign-up.
var blockedEmailDomains = map[string]bool{
	"gmail.com":      true,
	"hotmail.com":    true,
	"outlook.com":    true,
	"yahoo.com":      true,
	"sapo.pt":        true,
	"mail.com":       true,
	"live.com":       true,
	"icloud.com":     true,
	"protonmail.com": true,
	"aol.com":        true,
	"zoho.com":       true,
	"yandex.com":     true,
	"gmx.com":        true,
	"tutanota.com":   true,
	"hotmail.pt":     true,
	"outlook.pt":     true,
	"yahoo.pt":       true,
	"msn.com":        true,
	"me.com":         true,
	"proton.me":      true,
}

// isPersonalEmail checks if an email address belongs to a personal domain.
func isPersonalEmail(emailAddr string) bool {
	parts := strings.Split(emailAddr, "@")
	if len(parts) != 2 {
		return true // invalid email, treat as personal
	}
	domain := strings.ToLower(parts[1])
	return blockedEmailDomains[domain]
}

// RecruiterApply handles the public recruiter application form submission.
// POST /api/recruiters/apply
func RecruiterApply(c *gin.Context) {
	var input struct {
		FullName           string `json:"full_name" binding:"required"`
		CompanyName        string `json:"company_name" binding:"required"`
		Email              string `json:"email" binding:"required,email"`
		CompanyURL         string `json:"company_url" binding:"required,url"`
		VacancyDescription string `json:"vacancy_description"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Campos obrigatórios em falta.", "details": err.Error()})
		return
	}

	input.Email = strings.ToLower(strings.TrimSpace(input.Email))

	// Validate professional email
	if isPersonalEmail(input.Email) {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Por favor usa o teu email profissional.",
			"field": "email",
		})
		return
	}

	// Validate URL has protocol
	if !strings.HasPrefix(input.CompanyURL, "http://") && !strings.HasPrefix(input.CompanyURL, "https://") {
		input.CompanyURL = "https://" + input.CompanyURL
	}

	// Check if email already exists
	var existing models.Recruiter
	if err := database.DB.Where("email = ?", input.Email).First(&existing).Error; err == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Este email já está registado."})
		return
	}

	// Determine auto or manual approval
	status := "pending_manual"
	if !isPersonalEmail(input.Email) {
		status = "pending_auto"
	}

	recruiter := models.Recruiter{
		FullName:           strings.TrimSpace(input.FullName),
		CompanyName:        strings.TrimSpace(input.CompanyName),
		Email:              input.Email,
		CompanyURL:         input.CompanyURL,
		VacancyDescription: strings.TrimSpace(input.VacancyDescription),
		Status:             status,
	}

	if err := database.DB.Create(&recruiter).Error; err != nil {
		log.Printf("[recruiter] Erro ao criar recrutador: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao processar pedido."})
		return
	}

	log.Printf("[recruiter] Novo pedido: %s (%s) — status: %s", recruiter.CompanyName, recruiter.Email, status)

	// Auto-approve corporate email domains immediately
	if status == "pending_auto" {
		go func() {
			if err := approveRecruiter(recruiter.ID); err != nil {
				log.Printf("[recruiter] Erro na aprovação automática de %s: %v", recruiter.Email, err)
			}
		}()
	}

	// Send confirmation email
	go email.SendRecruiterReceived(recruiter.FullName, recruiter.Email)

	c.JSON(http.StatusCreated, gin.H{
		"message": "Pedido recebido! Vamos analisar e enviar-te um email em breve.",
	})
}

// approveRecruiter creates a Firebase user, sets recruiter claims, generates sign-in link, and sends approval email.
func approveRecruiter(recruiterID string) error {
	var recruiter models.Recruiter
	if err := database.DB.Where("id = ?", recruiterID).First(&recruiter).Error; err != nil {
		return err
	}

	// 1. Create Firebase user (or get existing)
	uid, err := middleware.CreateFirebaseUser(recruiter.Email, recruiter.FullName)
	if err != nil {
		return err
	}

	// 2. Set custom claims
	if err := middleware.SetRecruiterClaims(uid, recruiter.ID); err != nil {
		return err
	}

	// 3. Generate Firebase sign-in link
	continueURL := config.AppConfig.FrontendURL + "/recruiter/auth?email=" + url.QueryEscape(recruiter.Email)
	signInLink, err := middleware.GenerateSignInLink(recruiter.Email, continueURL)
	if err != nil {
		return err
	}

	// 4. Update recruiter record
	now := time.Now()
	if err := database.DB.Model(&recruiter).Updates(map[string]interface{}{
		"firebase_uid": uid,
		"status":       "approved",
		"approved_at":  now,
	}).Error; err != nil {
		return err
	}

	// 5. Send approval email
	if err := email.SendRecruiterApproved(recruiter.FullName, recruiter.Email, signInLink); err != nil {
		log.Printf("[recruiter] Erro ao enviar email de aprovação para %s: %v", recruiter.Email, err)
	}

	log.Printf("[recruiter] Aprovado: %s (%s)", recruiter.CompanyName, recruiter.Email)
	return nil
}

// GetRecruiterProfile returns the authenticated recruiter's profile.
// GET /api/recruiter/profile
func GetRecruiterProfile(c *gin.Context) {
	recruiterID := c.GetString("recruiter_id")

	var recruiter models.Recruiter
	if err := database.DB.Where("id = ?", recruiterID).First(&recruiter).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recrutador não encontrado."})
		return
	}

	c.JSON(http.StatusOK, recruiter)
}

// ── Admin Recruiter Management ──────────────────────────────

// AdminListRecruiters lists recruiters with optional status filter.
// GET /api/admin/recruiters?status=pending_manual
func AdminListRecruiters(c *gin.Context) {
	status := c.Query("status")

	query := database.DB.Order("created_at DESC")
	if status != "" {
		query = query.Where("status = ?", status)
	}

	var recruiters []models.Recruiter
	if err := query.Find(&recruiters).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao listar recrutadores."})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"recruiters": recruiters,
		"count":      len(recruiters),
	})
}

// AdminApproveRecruiter manually approves a pending recruiter.
// POST /api/admin/recruiters/:id/approve
func AdminApproveRecruiter(c *gin.Context) {
	id := c.Param("id")

	var recruiter models.Recruiter
	if err := database.DB.Where("id = ?", id).First(&recruiter).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recrutador não encontrado."})
		return
	}

	if recruiter.Status == "approved" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Recrutador já aprovado."})
		return
	}

	if err := approveRecruiter(id); err != nil {
		log.Printf("[admin] Erro ao aprovar recrutador %s: %v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao aprovar recrutador."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Recrutador aprovado com sucesso."})
}

// AdminRejectRecruiter rejects a pending recruiter and sends notification email.
// POST /api/admin/recruiters/:id/reject
func AdminRejectRecruiter(c *gin.Context) {
	id := c.Param("id")

	var recruiter models.Recruiter
	if err := database.DB.Where("id = ?", id).First(&recruiter).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recrutador não encontrado."})
		return
	}

	if err := database.DB.Model(&recruiter).Update("status", "rejected").Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao rejeitar recrutador."})
		return
	}

	// Send rejection email
	go email.SendRecruiterRejected(recruiter.FullName, recruiter.Email)

	log.Printf("[admin] Recrutador rejeitado: %s (%s)", recruiter.CompanyName, recruiter.Email)
	c.JSON(http.StatusOK, gin.H{"message": "Recrutador rejeitado."})
}

// AdminGetPendingRecruitersCount returns the count of pending recruiters.
// GET /api/admin/recruiters/pending-count
func AdminGetPendingRecruitersCount(c *gin.Context) {
	var count int64
	database.DB.Model(&models.Recruiter{}).Where("status IN ?", []string{"pending_manual", "pending_auto"}).Count(&count)
	c.JSON(http.StatusOK, gin.H{"count": count})
}
