package handlers

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/audit"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/middleware"
	"github.com/paiva/SkillBridge/Backend/internal/models"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

// --------------------------------------------------
// RATE LIMITING
// --------------------------------------------------

// totpAttempt tracks TOTP verification attempts
type totpAttempt struct {
	Timestamp time.Time
	IP        string
}

// totpAttempts stores recent TOTP attempts per UID
var totpAttempts = make(map[string][]totpAttempt)
var totpAttemptsLock sync.RWMutex

// checkTOTPRateLimit verifies if a user has exceeded TOTP rate limits
func checkTOTPRateLimit(uid string, ip string) bool {
	totpAttemptsLock.Lock()
	defer totpAttemptsLock.Unlock()

	now := time.Now()
	cutoff := now.Add(-time.Duration(config.AppConfig.TOTPRateLimitWindow) * time.Second)

	// Get attempts for this user
	attempts := totpAttempts[uid]

	// Filter out old attempts
	var recentAttempts []totpAttempt
	for _, attempt := range attempts {
		if attempt.Timestamp.After(cutoff) {
			recentAttempts = append(recentAttempts, attempt)
		}
	}

	// Update the map with filtered attempts
	totpAttempts[uid] = recentAttempts

	// Check if limit exceeded
	if len(recentAttempts) >= config.AppConfig.TOTPMaxAttempts {
		return false // Rate limit exceeded
	}

	// Add current attempt
	totpAttempts[uid] = append(recentAttempts, totpAttempt{
		Timestamp: now,
		IP:        ip,
	})

	return true // Within rate limit
}

// --------------------------------------------------
// TOTP HANDLERS
// --------------------------------------------------

// SetupTOTP generates a new TOTP secret and QR code for the authenticated admin user
//
// @Summary      Setup TOTP 2FA
// @Description  Generates TOTP secret and QR code for Google Authenticator setup
// @Tags         totp
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /admin/totp/setup [post]
func SetupTOTP(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	// Verify user is admin
	if !config.IsAdmin(firebaseUID) {
		audit.LogUnauthorized(c, "Not an admin", "TOTP Setup")
		c.JSON(http.StatusForbidden, gin.H{"error": "Acesso restrito a administradores."})
		return
	}

	// Get user from database
	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		audit.LogFailure(c, audit.ActionTOTPSetup, "User not found")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	// Generate TOTP key
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      "SkillBridge",
		AccountName: user.Email,
		Period:      30,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		audit.LogFailure(c, audit.ActionTOTPSetup, "Failed to generate TOTP key")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao gerar TOTP secret."})
		return
	}

	// Generate QR code
	var buf bytes.Buffer
	img, err := key.Image(300, 300)
	if err != nil {
		audit.LogFailure(c, audit.ActionTOTPSetup, "Failed to generate QR code")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao gerar QR code."})
		return
	}

	if err := png.Encode(&buf, img); err != nil {
		audit.LogFailure(c, audit.ActionTOTPSetup, "Failed to encode QR code")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao codificar QR code."})
		return
	}

	// Convert to base64
	qrCode := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	// Save secret to database (but don't enable TOTP yet - requires verification)
	user.TOTPSecret = key.Secret()
	user.TOTPEnabled = false // Will be enabled after first successful verification
	if err := database.DB.Save(&user).Error; err != nil {
		audit.LogFailure(c, audit.ActionTOTPSetup, "Failed to save TOTP secret to database")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao guardar TOTP secret."})
		return
	}

	audit.Log(c, audit.ActionTOTPSetup, "TOTP secret generated successfully")

	c.JSON(http.StatusOK, gin.H{
		"secret":  key.Secret(),
		"qr_code": qrCode,
		"message": "Scan the QR code with Google Authenticator and verify with a code to enable TOTP.",
	})
}

// VerifyTOTP verifies a TOTP code and enables TOTP if it's the first verification
//
// @Summary      Verify TOTP code
// @Description  Verifies a TOTP code from Google Authenticator
// @Tags         totp
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        input  body  object{code=string}  true  "TOTP code from authenticator app"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Router       /admin/totp/verify [post]
func VerifyTOTP(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	// Verify user is admin
	if !config.IsAdmin(firebaseUID) {
		audit.LogUnauthorized(c, "Not an admin", "TOTP Verify")
		c.JSON(http.StatusForbidden, gin.H{"error": "Acesso restrito a administradores."})
		return
	}

	// Check rate limit
	clientIP := c.ClientIP()
	if !checkTOTPRateLimit(firebaseUID, clientIP) {
		audit.LogAction(c, audit.ActionRateLimited, "TOTP rate limit exceeded: %d attempts in %d seconds",
			config.AppConfig.TOTPMaxAttempts, config.AppConfig.TOTPRateLimitWindow)
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": "Demasiadas tentativas. Aguarde antes de tentar novamente.",
		})
		return
	}

	var input struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		audit.LogFailure(c, audit.ActionTOTPVerify, "Missing TOTP code")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Código TOTP obrigatório."})
		return
	}

	// Get user from database
	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		audit.LogFailure(c, audit.ActionTOTPVerify, "User not found")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	// Check if TOTP secret exists
	if user.TOTPSecret == "" {
		audit.LogFailure(c, audit.ActionTOTPVerify, "TOTP not configured")
		c.JSON(http.StatusBadRequest, gin.H{"error": "TOTP não configurado. Execute /admin/totp/setup primeiro."})
		return
	}

	// Verify TOTP code
	valid := totp.Validate(input.Code, user.TOTPSecret)
	if !valid {
		audit.LogFailure(c, audit.ActionTOTPVerify, "Invalid TOTP code provided")
		c.JSON(http.StatusForbidden, gin.H{"error": "Código TOTP inválido."})
		return
	}

	// If this is the first successful verification, enable TOTP
	wasEnabled := user.TOTPEnabled
	if !user.TOTPEnabled {
		user.TOTPEnabled = true
		if err := database.DB.Save(&user).Error; err != nil {
			audit.LogFailure(c, audit.ActionTOTPVerify, "Failed to enable TOTP in database")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao ativar TOTP."})
			return
		}
	}

	// Create TOTP session
	middleware.CreateTOTPSession(firebaseUID, clientIP, c.GetHeader("User-Agent"))

	audit.LogAction(c, audit.ActionTOTPVerify, "TOTP verified successfully (enabled=%v)", user.TOTPEnabled)

	c.JSON(http.StatusOK, gin.H{
		"valid":   true,
		"enabled": user.TOTPEnabled,
		"message": func() string {
			if !wasEnabled {
				return "TOTP ativado com sucesso!"
			}
			return "Código TOTP válido."
		}(),
	})
}

// CheckTOTPStatus checks if TOTP is enabled for the authenticated admin user
//
// @Summary      Check TOTP status
// @Description  Returns whether TOTP is enabled for the current admin user
// @Tags         totp
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]string
// @Router       /admin/totp/status [get]
func CheckTOTPStatus(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	// Verify user is admin
	if !config.IsAdmin(firebaseUID) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Acesso restrito a administradores."})
		return
	}

	// Get user from database
	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"totp_enabled": user.TOTPEnabled,
		"has_secret":   user.TOTPSecret != "",
	})
}

// DisableTOTP disables TOTP for the authenticated admin user
//
// @Summary      Disable TOTP 2FA
// @Description  Disables TOTP and removes the secret
// @Tags         totp
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        input  body  object{code=string}  true  "Current valid TOTP code to confirm"
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Router       /admin/totp/disable [post]
func DisableTOTP(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	// Verify user is admin
	if !config.IsAdmin(firebaseUID) {
		audit.LogUnauthorized(c, "Not an admin", "TOTP Disable")
		c.JSON(http.StatusForbidden, gin.H{"error": "Acesso restrito a administradores."})
		return
	}

	var input struct {
		Code string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		audit.LogFailure(c, audit.ActionTOTPDisable, "Missing TOTP code")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Código TOTP obrigatório para desativar."})
		return
	}

	// Get user from database
	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		audit.LogFailure(c, audit.ActionTOTPDisable, "User not found")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	// Verify TOTP code before disabling
	if user.TOTPSecret != "" {
		valid := totp.Validate(input.Code, user.TOTPSecret)
		if !valid {
			audit.LogFailure(c, audit.ActionTOTPDisable, "Invalid TOTP code provided")
			c.JSON(http.StatusForbidden, gin.H{"error": "Código TOTP inválido."})
			return
		}
	}

	// Disable TOTP and clear secret
	user.TOTPEnabled = false
	user.TOTPSecret = ""
	if err := database.DB.Save(&user).Error; err != nil {
		audit.LogFailure(c, audit.ActionTOTPDisable, "Failed to disable TOTP in database")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao desativar TOTP."})
		return
	}

	// Clear session
	middleware.ClearTOTPSession(firebaseUID)

	audit.Log(c, audit.ActionTOTPDisable, "TOTP disabled successfully")

	c.JSON(http.StatusOK, gin.H{
		"message": "TOTP desativado com sucesso.",
	})
}
