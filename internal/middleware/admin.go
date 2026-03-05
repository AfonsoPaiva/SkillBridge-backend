package middleware

import (
	"log"
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
		log.Printf("[IP Whitelist] No IP restrictions configured - allowing all IPs")
		return true
	}

	log.Printf("[IP Whitelist] Checking IP %s against whitelist: %v", ip, config.AppConfig.AdminAllowedIPs)

	// Check if IP is in the whitelist
	for _, allowedIP := range config.AppConfig.AdminAllowedIPs {
		if allowedIP == ip {
			log.Printf("[IP Whitelist] IP %s matched whitelist entry %s - ALLOWED", ip, allowedIP)
			return true
		}
		// Support CIDR notation or wildcard patterns in the future
		// For now, exact match only
	}

	log.Printf("[IP Whitelist] IP %s not found in whitelist - DENIED", ip)
	return false
}

// IPWhitelistRequired checks if the client IP is in the admin whitelist
// Use this before AuthRequired for routes that should only be accessible from whitelisted IPs
func IPWhitelistRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := getClientIP(c)
		
		// Skip check if no whitelist is configured
		if len(config.AppConfig.AdminAllowedIPs) == 0 {
			c.Next()
			return
		}
		
		log.Printf("[IP Whitelist] Checking access to %s from IP %s", c.Request.URL.Path, clientIP)
		
		if !isIPAllowed(clientIP) {
			log.Printf("[IP Whitelist] Access DENIED to %s from IP %s (not in whitelist)", c.Request.URL.Path, clientIP)
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Acesso negado. Seu IP não está autorizado.",
			})
			c.Abort()
			return
		}
		
		log.Printf("[IP Whitelist] Access ALLOWED to %s from IP %s", c.Request.URL.Path, clientIP)
		c.Next()
	}
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
		log.Printf("[Admin] Checking IP whitelist for UID=%s IP=%s Path=%s", uid, clientIP, c.Request.URL.Path)
		if !isIPAllowed(clientIP) {
			audit.LogAction(c, audit.ActionUnauthorized,
				"IP not whitelisted: %s | Path: %s", clientIP, c.Request.URL.Path)
			log.Printf("[Admin] Access DENIED for UID=%s from IP=%s (not in whitelist)", uid, clientIP)
			c.JSON(http.StatusForbidden, gin.H{
				"error": "Acesso negado. Seu IP não está autorizado a acessar o painel administrativo.",
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
