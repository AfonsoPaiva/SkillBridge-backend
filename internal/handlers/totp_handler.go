package handlers

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/internal/audit"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/models"
	"github.com/pquerna/otp/totp"
)

// Rate limiting for TOTP verification attempts
var (
	totpAttemptsMux sync.Mutex
	totpAttempts    = make(map[string][]time.Time)
)

// checkTOTPRateLimit verifica se o utilizador excedeu o limite de tentativas
func checkTOTPRateLimit(uid, ip string) bool {
	totpAttemptsMux.Lock()
	defer totpAttemptsMux.Unlock()

	key := uid + ":" + ip
	now := time.Now()

	// Get rate limit config
	maxAttempts := 5
	if val := os.Getenv("TOTP_MAX_ATTEMPTS"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			maxAttempts = parsed
		}
	}

	windowSeconds := 60
	if val := os.Getenv("TOTP_RATE_LIMIT_WINDOW"); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			windowSeconds = parsed
		}
	}

	cutoff := now.Add(-time.Duration(windowSeconds) * time.Second)

	// Remove old attempts outside the window
	attempts := totpAttempts[key]
	validAttempts := []time.Time{}
	for _, t := range attempts {
		if t.After(cutoff) {
			validAttempts = append(validAttempts, t)
		}
	}

	// Check if limit exceeded
	if len(validAttempts) >= maxAttempts {
		return false // Rate limit exceeded
	}

	// Add current attempt
	validAttempts = append(validAttempts, now)
	totpAttempts[key] = validAttempts

	return true // Within rate limit
}

// TOTPSetup gera um novo segredo TOTP (apenas se não existir) e retorna o segredo
func TOTPSetup(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")
	if firebaseUID == "" {
		log.Printf("[TOTP Setup] Missing firebase_uid in context")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Não autenticado"})
		return
	}

	// Get user
	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		log.Printf("[TOTP Setup] User not found for UID %s: %v", firebaseUID, err)
		audit.LogFailure(c, audit.ActionTOTPSetup, "User not found")
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado"})
		return
	}

	// If user already has a TOTP secret, return it (don't regenerate)
	if user.TOTPSecret != "" {
		log.Printf("[TOTP Setup] Returning existing TOTP secret for UID %s", firebaseUID)
		c.JSON(http.StatusOK, gin.H{
			"secret":      user.TOTPSecret,
			"totp_enabled": user.TOTPEnabled,
		})
		return
	}

	// Generate new TOTP key (first time only)
	issuer := "SkillBridge"
	if customIssuer := os.Getenv("TOTP_ISSUER"); customIssuer != "" {
		issuer = customIssuer
	}

	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      issuer,
		AccountName: user.Email,
		SecretSize:  20,
	})

	if err != nil {
		log.Printf("[TOTP Setup] Failed to generate TOTP key for UID %s: %v", firebaseUID, err)
		audit.LogFailure(c, audit.ActionTOTPSetup, "Failed to generate TOTP key")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao gerar chave TOTP"})
		return
	}

	// Store TOTP secret in database (not enabled yet)
	user.TOTPSecret = key.Secret()
	user.TOTPEnabled = false
	if err := database.DB.Save(&user).Error; err != nil {
		log.Printf("[TOTP Setup] Failed to save TOTP secret for UID %s: %v", firebaseUID, err)
		audit.LogFailure(c, audit.ActionTOTPSetup, "Failed to save TOTP secret")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao salvar segredo TOTP"})
		return
	}

	audit.LogAction(c, audit.ActionTOTPSetup, "TOTP setup initiated for user ID=%d", user.ID)
	log.Printf("[TOTP Setup] Successfully generated TOTP for UID %s", firebaseUID)

	// Return only the secret (no QR code)
	c.JSON(http.StatusOK, gin.H{
		"secret":      key.Secret(),
		"totp_enabled": false,
	})
}

// TOTPVerify valida o código TOTP e ativa-o se for a primeira verificação
func TOTPVerify(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")
	if firebaseUID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Não autenticado"})
		return
	}

	clientIP := c.ClientIP()

	// Check rate limit
	if !checkTOTPRateLimit(firebaseUID, clientIP) {
		audit.LogAction(c, audit.ActionRateLimited, "TOTP rate limit exceeded")
		log.Printf("[TOTP Verify] Rate limit exceeded for UID %s from IP %s", firebaseUID, clientIP)
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "Demasiadas tentativas. Tente novamente mais tarde."})
		return
	}

	var req struct {
		Code string `json:"code" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Código inválido"})
		return
	}

	// Get user's TOTP secret
	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		log.Printf("[TOTP Verify] User not found for UID %s", firebaseUID)
		audit.LogFailure(c, audit.ActionTOTPVerify, "User not found")
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado"})
		return
	}

	if user.TOTPSecret == "" {
		log.Printf("[TOTP Verify] No TOTP secret found for UID %s", firebaseUID)
		audit.LogFailure(c, audit.ActionTOTPVerify, "TOTP not configured")
		c.JSON(http.StatusBadRequest, gin.H{"error": "TOTP não configurado"})
		return
	}

	// Validate TOTP code
	valid := totp.Validate(req.Code, user.TOTPSecret)
	if !valid {
		audit.LogAction(c, audit.ActionTOTPVerifyFailed, "Invalid TOTP code for user ID=%d", user.ID)
		log.Printf("[TOTP Verify] Invalid code for UID %s", firebaseUID)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Código inválido"})
		return
	}

	// If first verification, enable TOTP
	now := time.Now()
	if !user.TOTPEnabled {
		user.TOTPEnabled = true
		user.TOTPVerifiedAt = &now
		if err := database.DB.Save(&user).Error; err != nil {
			log.Printf("[TOTP Verify] Failed to enable TOTP for UID %s: %v", firebaseUID, err)
			audit.LogFailure(c, audit.ActionTOTPVerify, "Failed to enable TOTP")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao ativar TOTP"})
			return
		}
		audit.LogAction(c, audit.ActionTOTPEnabled, "TOTP enabled for user ID=%d", user.ID)
		log.Printf("[TOTP Verify] TOTP enabled and verified for UID %s", firebaseUID)
	} else {
		// Update verification timestamp
		user.TOTPVerifiedAt = &now
		if err := database.DB.Save(&user).Error; err != nil {
			log.Printf("[TOTP Verify] Failed to update verification timestamp for UID %s: %v", firebaseUID, err)
		}
		log.Printf("[TOTP Verify] TOTP verification timestamp updated for UID %s", firebaseUID)
	}

	audit.LogAction(c, audit.ActionTOTPVerify, "TOTP verification successful for user ID=%d", user.ID)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"enabled": true,
	})
}

// TOTPDisable desativa o TOTP para o utilizador
func TOTPDisable(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")
	if firebaseUID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Não autenticado"})
		return
	}

	// Get user
	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		log.Printf("[TOTP Disable] User not found for UID %s: %v", firebaseUID, err)
		audit.LogFailure(c, audit.ActionTOTPDisable, "User not found")
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado"})
		return
	}

	// Disable TOTP
	user.TOTPSecret = ""
	user.TOTPEnabled = false
	user.TOTPVerifiedAt = nil
	if err := database.DB.Save(&user).Error; err != nil {
		log.Printf("[TOTP Disable] Failed to disable TOTP for UID %s: %v", firebaseUID, err)
		audit.LogFailure(c, audit.ActionTOTPDisable, "Failed to save")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao desativar TOTP"})
		return
	}

	audit.LogAction(c, audit.ActionTOTPDisable, "TOTP disabled for user ID=%d", user.ID)
	log.Printf("[TOTP Disable] TOTP disabled and verification cleared for UID %s", firebaseUID)

	c.JSON(http.StatusOK, gin.H{"success": true})
}

// TOTPStatus retorna o estado atual do TOTP do utilizador
func TOTPStatus(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")
	if firebaseUID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Não autenticado"})
		return
	}

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		log.Printf("[TOTP Status] Failed to get status for UID %s: %v", firebaseUID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao verificar estado TOTP"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"totp_enabled": user.TOTPEnabled,
		"has_secret":   user.TOTPSecret != "",
		"verified":     user.TOTPVerifiedAt != nil,
		"verified_at":  user.TOTPVerifiedAt,
	})
}
