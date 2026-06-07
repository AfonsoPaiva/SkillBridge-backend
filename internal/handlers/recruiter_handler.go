package handlers

import (
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/email"
	"github.com/paiva/SkillBridge/Backend/internal/jobs"
	"github.com/paiva/SkillBridge/Backend/internal/middleware"
	"github.com/paiva/SkillBridge/Backend/internal/models"
	"github.com/paiva/SkillBridge/Backend/internal/storage"

	"net/url"
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

// createRecruiterToken generates a new 72-hour access token for a recruiter.
func createRecruiterToken(recruiterID string) (string, error) {
	return jobs.CreateRecruiterToken(recruiterID)
}

// buildRecruiterAccessLink constructs the frontend URL for recruiter authentication.
func buildRecruiterAccessLink(token string) string {
	return config.AppConfig.FrontendURL + "/recruiter/auth?token=" + token
}

// fetchAndUploadClearbitLogo fetches a company logo. We use icon.horse as it's free and reliable.
func fetchAndUploadClearbitLogo(companyURL string, uid string) string {
	if companyURL == "" {
		return ""
	}
	parsedURL, err := url.Parse(companyURL)
	if err != nil {
		return ""
	}
	domain := strings.TrimPrefix(parsedURL.Hostname(), "www.")
	if domain == "" {
		return ""
	}

	return "https://icon.horse/icon/" + domain
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
		// Recruiter already exists — check status
		if existing.Status == "approved" {
			// Already approved: generate new access token and send returning-user email
			go func() {
				token, err := createRecruiterToken(existing.ID)
				if err != nil {
					log.Printf("[recruiter] Erro ao gerar token para recrutador existente %s: %v", existing.Email, err)
					return
				}
				accessLink := buildRecruiterAccessLink(token)
				if err := email.SendRecruiterReturning(existing.FullName, existing.Email, accessLink); err != nil {
					log.Printf("[recruiter] Erro ao enviar email de acesso para %s: %v", existing.Email, err)
				}
			}()
			c.JSON(http.StatusConflict, gin.H{
				"error":   "Este email já está registado e aprovado.",
				"message": "Enviámos um novo link de acesso para o seu email.",
			})
			return
		}
		c.JSON(http.StatusConflict, gin.H{"error": "Este email já está registado."})
		return
	}

	// Todos os pedidos requerem agora aprovação manual
	status := "pending_manual"

	var recruiter models.Recruiter
	// Check if a dummy recruiter exists for this company
	var dummy models.Recruiter
	errDummy := database.DB.Where("LOWER(company_name) = ? AND (email LIKE '%@dummy.skillbridge.pt' OR email LIKE '%@auto-scraped.com')", strings.ToLower(strings.TrimSpace(input.CompanyName))).First(&dummy).Error

	if errDummy == nil {
		dummy.FullName = strings.TrimSpace(input.FullName)
		dummy.Email = input.Email
		dummy.CompanyURL = input.CompanyURL
		dummy.VacancyDescription = strings.TrimSpace(input.VacancyDescription)
		dummy.Status = status

		if err := database.DB.Save(&dummy).Error; err != nil {
			log.Printf("[recruiter] Erro ao atualizar recrutador dummy: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao processar pedido."})
			return
		}
		recruiter = dummy
	} else {
		recruiter = models.Recruiter{
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
	}

	// Auto-fetch logo and save it to the buckets asynchronously
	go func(recID string, url string) {
		logoURL := fetchAndUploadClearbitLogo(url, recID)
		if logoURL != "" {
			database.DB.Model(&models.Recruiter{}).Where("id = ?", recID).Update("logo_url", logoURL)
		}
	}(recruiter.ID, recruiter.CompanyURL)

	log.Printf("[recruiter] Novo pedido: %s (%s) — status: %s", recruiter.CompanyName, recruiter.Email, status)

	// Enviar email de confirmação para o recrutador
	go email.SendRecruiterReceived(recruiter.FullName, recruiter.Email)

	// Enviar email de notificação para o administrador
	go email.SendAdminNewRecruiterNotification(recruiter.CompanyName, recruiter.FullName)

	c.JSON(http.StatusCreated, gin.H{
		"message": "Pedido recebido! Vamos analisar e enviar-te um email em breve.",
	})
}

// RecruiterRequestLink handles requests from approved recruiters to get a new sign-in link.
// POST /api/recruiters/request-link
func RecruiterRequestLink(c *gin.Context) {
	var input struct {
		Email string `json:"email" binding:"required,email"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Email inválido."})
		return
	}

	input.Email = strings.ToLower(strings.TrimSpace(input.Email))

	var recruiter models.Recruiter
	if err := database.DB.Where("email = ?", input.Email).First(&recruiter).Error; err != nil {
		// Do not leak existence
		c.JSON(http.StatusOK, gin.H{"message": "Se o email estiver aprovado, receberás um link de acesso em breve."})
		return
	}

	if recruiter.Status != "approved" {
		c.JSON(http.StatusOK, gin.H{"message": "Se o email estiver aprovado, receberás um link de acesso em breve."})
		return
	}

	// Generate new secure access token (72h)
	token, err := createRecruiterToken(recruiter.ID)
	if err != nil {
		log.Printf("[recruiter] Erro ao gerar token para %s: %v", recruiter.Email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao gerar link."})
		return
	}

	accessLink := buildRecruiterAccessLink(token)

	// Send returning recruiter email (different from first-time approval)
	if err := email.SendRecruiterReturning(recruiter.FullName, recruiter.Email, accessLink); err != nil {
		log.Printf("[recruiter] Erro ao enviar link de login para %s: %v", recruiter.Email, err)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Se o email estiver aprovado, receberás um link de acesso em breve."})
}

// RecruiterVerifyToken validates a recruiter access token and returns a Firebase custom token.
// POST /api/recruiters/verify-token
func RecruiterVerifyToken(c *gin.Context) {
	var input struct {
		Token string `json:"token" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Token em falta."})
		return
	}

	// Find the token
	var rt models.RecruiterToken
	if err := database.DB.Where("token = ?", input.Token).First(&rt).Error; err != nil {
		log.Printf("[recruiter] Token inválido tentado")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Link inválido ou expirado. Solicite um novo acesso."})
		return
	}

	// Check expiration
	if time.Now().After(rt.ExpiresAt) {
		log.Printf("[recruiter] Token expirado para recruiter_id=%s", rt.RecruiterID)
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "Link expirado. Solicite um novo acesso.",
			"expired": true,
		})
		return
	}

	// Get the recruiter
	var recruiter models.Recruiter
	if err := database.DB.Where("id = ?", rt.RecruiterID).First(&recruiter).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recrutador não encontrado."})
		return
	}

	if recruiter.Status != "approved" {
		c.JSON(http.StatusForbidden, gin.H{"error": "Conta não aprovada."})
		return
	}

	// Check Firebase UID exists
	if recruiter.FirebaseUID == nil || *recruiter.FirebaseUID == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Conta Firebase não configurada. Contacte o suporte."})
		return
	}

	// Generate Firebase custom token
	customToken, err := middleware.GenerateCustomToken(*recruiter.FirebaseUID)
	if err != nil {
		log.Printf("[recruiter] Erro ao gerar custom token para %s: %v", recruiter.Email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro na autenticação."})
		return
	}

	// Increment usage count
	database.DB.Model(&rt).Update("used_count", rt.UsedCount+1)

	log.Printf("[recruiter] Token verificado com sucesso para %s (uso #%d)", recruiter.Email, rt.UsedCount+1)

	c.JSON(http.StatusOK, gin.H{
		"custom_token": customToken,
		"recruiter": gin.H{
			"id":           recruiter.ID,
			"full_name":    recruiter.FullName,
			"company_name": recruiter.CompanyName,
			"email":        recruiter.Email,
		},
	})
}

// approveRecruiter creates a Firebase user, sets recruiter claims, generates access token, and sends approval email.
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

	// 3. Generate secure access token (72h)
	token, err := createRecruiterToken(recruiter.ID)
	if err != nil {
		return err
	}

	accessLink := buildRecruiterAccessLink(token)

	// 4. Update recruiter record
	now := time.Now()
	if err := database.DB.Model(&recruiter).Updates(map[string]interface{}{
		"firebase_uid": uid,
		"status":       "approved",
		"approved_at":  now,
	}).Error; err != nil {
		return err
	}

	// 5. Send approval email with secure access link
	if err := email.SendRecruiterApproved(recruiter.FullName, recruiter.Email, accessLink); err != nil {
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

// UpdateRecruiterProfile updates the authenticated recruiter's profile.
// PUT /api/recruiter/profile
func UpdateRecruiterProfile(c *gin.Context) {
	recruiterID := c.GetString("recruiter_id")

	var input struct {
		LogoURL    *string `json:"logo_url"`
		CompanyURL *string `json:"company_url"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados inválidos."})
		return
	}

	var recruiter models.Recruiter
	if err := database.DB.Where("id = ?", recruiterID).First(&recruiter).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recrutador não encontrado."})
		return
	}

	updates := make(map[string]interface{})
	if input.LogoURL != nil {
		newLogoURL := *input.LogoURL
		if recruiter.LogoURL != "" && recruiter.LogoURL != newLogoURL {
			oldObjectName := extractGCSObjectName(recruiter.LogoURL)
			if oldObjectName != "" {
				if err := storage.DeleteFile(oldObjectName); err != nil {
					log.Printf("Warning: Failed to delete old recruiter logo %s: %v", oldObjectName, err)
				}
			}
		}
		updates["logo_url"] = newLogoURL
	}
	if input.CompanyURL != nil {
		newCompanyURL := *input.CompanyURL
		updates["company_url"] = newCompanyURL

		// If URL changed and we aren't explicitly updating the logo to something else
		if newCompanyURL != recruiter.CompanyURL && (input.LogoURL == nil || *input.LogoURL == recruiter.LogoURL) {
			newLogoURL := fetchAndUploadClearbitLogo(newCompanyURL, recruiter.ID)
			if newLogoURL != "" {
				updates["logo_url"] = newLogoURL
				if recruiter.LogoURL != "" && recruiter.LogoURL != newLogoURL {
					oldObjectName := extractGCSObjectName(recruiter.LogoURL)
					if oldObjectName != "" {
						if err := storage.DeleteFile(oldObjectName); err != nil {
							log.Printf("Warning: Failed to delete old recruiter logo %s: %v", oldObjectName, err)
						}
					}
				}
			}
		}
	}

	if err := database.DB.Model(&recruiter).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao atualizar perfil."})
		return
	}

	database.DB.Where("id = ?", recruiterID).First(&recruiter)
	c.JSON(http.StatusOK, recruiter)
}

// DeleteRecruiterProfile allows a recruiter to permanently delete their account.
// DELETE /api/recruiter/profile
func DeleteRecruiterProfile(c *gin.Context) {
	recruiterID := c.GetString("recruiter_id")

	var recruiter models.Recruiter
	if err := database.DB.Where("id = ?", recruiterID).First(&recruiter).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recrutador não encontrado."})
		return
	}

	// Delete Firebase user if possible
	if recruiter.FirebaseUID != nil && *recruiter.FirebaseUID != "" {
		if err := middleware.DeleteUser(*recruiter.FirebaseUID); err != nil {
			log.Printf("[recruiter] Erro ao eliminar utilizador Firebase uid=%s do recrutador: %v", *recruiter.FirebaseUID, err)
		}
	}

	// Delete associated tokens
	database.DB.Where("recruiter_id = ?", recruiterID).Delete(&models.RecruiterToken{})

	// Delete recruiter logo from GCS if it exists
	if recruiter.LogoURL != "" {
		objectName := extractGCSObjectName(recruiter.LogoURL)
		if objectName != "" {
			if err := storage.DeleteFile(objectName); err != nil {
				log.Printf("Warning: Failed to delete recruiter logo %s: %v", objectName, err)
			}
		}
	}

	// Delete associated vacancies
	database.DB.Where("recruiter_id = ?", recruiterID).Delete(&models.Vacancy{})

	// Delete recruiter from database
	if err := database.DB.Delete(&recruiter).Error; err != nil {
		log.Printf("[recruiter] Erro ao eliminar conta do recrutador: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao eliminar conta."})
		return
	}

	log.Printf("[recruiter] Conta eliminada por iniciativa do recrutador: ID=%s", recruiterID)
	c.JSON(http.StatusOK, gin.H{"message": "Conta eliminada com sucesso."})
}

// ── Admin Recruiter Management ──────────────────────────────

// AdminListRecruiters lists recruiters with optional status filter.
// GET /api/admin/recruiters?status=pending_manual
func AdminListRecruiters(c *gin.Context) {
	status := c.Query("status")

	query := database.DB.Order("created_at DESC")
	if status != "" && status != "all" {
		if status == "pending" {
			query = query.Where("status IN ?", []string{"pending_manual", "pending_auto"})
		} else {
			query = query.Where("status = ?", status)
		}
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

// AdminDeleteRecruiter permanently deletes a recruiter and their Firebase account.
// DELETE /api/admin/recruiters/:id
func AdminDeleteRecruiter(c *gin.Context) {
	id := c.Param("id")

	var recruiter models.Recruiter
	if err := database.DB.Where("id = ?", id).First(&recruiter).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recrutador não encontrado."})
		return
	}

	// Delete Firebase user if possible
	if recruiter.FirebaseUID != nil && *recruiter.FirebaseUID != "" {
		if err := middleware.DeleteUser(*recruiter.FirebaseUID); err != nil {
			log.Printf("[admin] Erro ao eliminar utilizador Firebase uid=%s do recrutador: %v", *recruiter.FirebaseUID, err)
		}
	}

	// Delete associated tokens
	database.DB.Where("recruiter_id = ?", id).Delete(&models.RecruiterToken{})

	// Delete recruiter logo from GCS if it exists
	if recruiter.LogoURL != "" {
		objectName := extractGCSObjectName(recruiter.LogoURL)
		if objectName != "" {
			if err := storage.DeleteFile(objectName); err != nil {
				log.Printf("Warning: Failed to delete recruiter logo %s: %v", objectName, err)
			}
		}
	}

	// Delete associated vacancies
	database.DB.Where("recruiter_id = ?", id).Delete(&models.Vacancy{})

	// Delete recruiter from database
	if err := database.DB.Delete(&recruiter).Error; err != nil {
		log.Printf("[admin] Erro ao eliminar recrutador: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao eliminar recrutador."})
		return
	}

	log.Printf("[admin] Recrutador eliminado: ID=%s, Empresa=%s, Email=%s", recruiter.ID, recruiter.CompanyName, recruiter.Email)
	c.JSON(http.StatusOK, gin.H{"message": "Recrutador eliminado com sucesso."})
}

// AdminResendRecruiterEmail resends the approval email with a fresh access link.
// POST /api/admin/recruiters/:id/resend-email
func AdminResendRecruiterEmail(c *gin.Context) {
	id := c.Param("id")

	var recruiter models.Recruiter
	if err := database.DB.Where("id = ?", id).First(&recruiter).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Recrutador não encontrado."})
		return
	}

	if recruiter.Status != "approved" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Apenas é possível reenviar email para recrutadores aprovados."})
		return
	}

	// Generate new secure access token (72h)
	token, err := createRecruiterToken(recruiter.ID)
	if err != nil {
		log.Printf("[admin] Erro ao gerar token para %s: %v", recruiter.Email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao gerar novo link de acesso."})
		return
	}

	accessLink := buildRecruiterAccessLink(token)

	// Send approval email again
	if err := email.SendRecruiterApproved(recruiter.FullName, recruiter.Email, accessLink); err != nil {
		log.Printf("[admin] Erro ao reenviar email para %s: %v", recruiter.Email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao enviar o email."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Email reenviado com sucesso."})
}

// AdminGetPendingRecruitersCount returns the count of pending recruiters.
// GET /api/admin/recruiters/pending-count
func AdminGetPendingRecruitersCount(c *gin.Context) {
	var count int64
	database.DB.Model(&models.Recruiter{}).Where("status IN ?", []string{"pending_manual", "pending_auto"}).Count(&count)
	c.JSON(http.StatusOK, gin.H{"count": count})
}
