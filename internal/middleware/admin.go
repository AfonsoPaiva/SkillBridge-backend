package middleware

import (
	"crypto/subtle"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
)

// AdminRequired verifica duas condições em simultâneo:
//  1. O Firebase UID está na lista de administradores (ADMIN_UIDS)
//  2. O header X-Admin-Key corresponde ao segredo ADMIN_SECRET_KEY
//
// Ambas têm de passar — basta uma falhar para o acesso ser negado.
// Deve ser usado após AuthRequired().
func AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. Verificar UID
		uid := c.GetString("firebase_uid")
		if !config.IsAdmin(uid) {
			c.JSON(http.StatusForbidden, gin.H{"error": "Acesso restrito a administradores."})
			c.Abort()
			return
		}

		// 2. Verificar chave secreta no header X-Admin-Key
		secret := config.AppConfig.AdminSecretKey
		if secret == "" {
			// Se não há chave configurada, bloquear por defeito (fail-secure)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Admin não configurado no servidor."})
			c.Abort()
			return
		}

		provided := c.GetHeader("X-Admin-Key")
		// subtle.ConstantTimeCompare evita timing attacks
		if subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
			c.JSON(http.StatusForbidden, gin.H{"error": "Chave de administrador inválida."})
			c.Abort()
			return
		}

		c.Next()
	}
}
