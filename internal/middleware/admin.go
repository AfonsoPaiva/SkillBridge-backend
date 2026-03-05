package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/audit"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/models"
)

// getClientIP extracts the real client IP from the request
func getClientIP(c *gin.Context) string {
	// Check X-Forwarded-For header (proxy/load balancer)
	xff := c.GetHeader("X-Forwarded-For")
	if xff != "" {
		// Take the first IP if multiple are present
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// Check X-Real-IP header
	xri := c.GetHeader("X-Real-IP")
	if xri != "" {
		return xri
	}

	// Fallback to direct client IP
	return c.ClientIP()
}

// isIPAllowed checks if an IP is in the allowed list
func isIPAllowed(ip string) bool {
	// If no IP whitelist is configured, allow all
	if len(config.AppConfig.AdminAllowedIPs) == 0 {
		return true
	}

	// Check if IP is in the whitelist
	for _, allowedIP := range config.AppConfig.AdminAllowedIPs {
		if allowedIP == ip {
			return true
		}
		// Support CIDR notation or wildcard patterns in the future
		// For now, exact match only
	}

	return false
}

// AdminRequired verifica se o utilizador é administrador e tem TOTP válido (se ativado).
// Deve ser usado após AuthRequired().
func AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 0. Get client IP for logging and IP whitelisting
		clientIP := getClientIP(c)

		// 1. Verificar se o Firebase UID está na lista de administradores
		uid := c.GetString("firebase_uid")
		if !config.IsAdmin(uid) {
			audit.LogUnauthorized(c, "UID not in admin list", c.Request.URL.Path)
			c.JSON(http.StatusForbidden, gin.H{"error": "Acesso restrito a administradores."})
			c.Abort()
			return
		}

		// 2. IP Whitelisting (if configured)
		if !isIPAllowed(clientIP) {
			audit.LogAction(c, audit.ActionUnauthorized,
				"IP not whitelisted: %s | Path: %s", clientIP, c.Request.URL.Path)
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Acesso negado. IP não autorizado.",
			})
			c.Abort()
			return
		}

		// 3. Verificar se o utilizador tem TOTP ativado
		var user models.User
		if err := database.DB.Where("firebase_uid = ?", uid).First(&user).Error; err != nil {
			audit.LogFailure(c, audit.ActionUnauthorized, "User not found in database")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Utilizador não encontrado."})
			c.Abort()
			return
		}

		// 4. Se TOTP está ativado, verificar se tem sessão TOTP válida
		if user.TOTPEnabled {
			if !ValidateTOTPSession(uid) {
				audit.LogAction(c, audit.ActionUnauthorized,
					"TOTP session expired or missing | Path: %s", c.Request.URL.Path)
				c.JSON(http.StatusForbidden, gin.H{
					"error":         "Autenticação TOTP necessária.",
					"requires_totp": true,
				})
				c.Abort()
				return
			}
		}

		// All checks passed - log successful admin access
		audit.LogAction(c, audit.ActionLogin, "Admin access granted | Path: %s", c.Request.URL.Path)

		c.Next()
	}
}
