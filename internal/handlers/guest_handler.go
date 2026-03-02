package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/models"
)

// validRoles is the set of allowed role values.
var validRoles = map[string]bool{
	"needs_help": true,
	"helper":     true,
}

// generateToken creates a cryptographically random 32-character hex token.
func generateToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// CreateGuestSession — cria ou atualiza uma sessão anónima de onboarding.
//
// @Summary      Criar sessão de utilizador anónimo
// @Description  Guarda as preferências de onboarding (role e área) para um visitante não autenticado. Devolve um token que o frontend deve persistir (localStorage). Se o token já existir, atualiza as preferências.
// @Tags         guest
// @Accept       json
// @Produce      json
// @Param        input  body  object{token=string,role=string}  true  "Preferências (token opcional; se omitido é criado um novo)"
// @Success      200  {object}  models.GuestSession
// @Failure      400  {object}  map[string]string
// @Router       /guest/session [post]
func CreateGuestSession(c *gin.Context) {
	var input struct {
		Token string `json:"token"` // optional: send existing token to update
		Role  string `json:"role" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate role
	if !validRoles[input.Role] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role inválido. Use 'needs_help' ou 'helper'."})
		return
	}

	// If a token was provided, try to update the existing session
	if input.Token != "" {
		var existing models.GuestSession
		if err := database.DB.Where("token = ? AND expires_at > ?", input.Token, time.Now()).
			First(&existing).Error; err == nil {
			existing.Role = input.Role
			existing.ExpiresAt = time.Now().Add(90 * 24 * time.Hour)
			database.DB.Save(&existing)
			c.JSON(http.StatusOK, existing)
			return
		}
	}

	// Create a new session
	token, err := generateToken()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao gerar token."})
		return
	}

	session := models.GuestSession{
		Token:     token,
		Role:      input.Role,
		ExpiresAt: time.Now().Add(90 * 24 * time.Hour),
	}

	if err := database.DB.Create(&session).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao guardar sessão."})
		return
	}

	c.JSON(http.StatusOK, session)
}

// GetGuestSession — devolve as preferências associadas a um token anónimo.
//
// @Summary      Obter sessão de utilizador anónimo
// @Description  Devolve as preferências de onboarding (role e área) de um visitante não autenticado pelo token
// @Tags         guest
// @Produce      json
// @Param        token  path  string  true  "Token da sessão anónima"
// @Success      200  {object}  models.GuestSession
// @Failure      404  {object}  map[string]string
// @Router       /guest/session/{token} [get]
func GetGuestSession(c *gin.Context) {
	token := c.Param("token")

	var session models.GuestSession
	if err := database.DB.Where("token = ? AND expires_at > ?", token, time.Now()).
		First(&session).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Sessão não encontrada ou expirada."})
		return
	}

	c.JSON(http.StatusOK, session)
}

// GetPlatformStats — devolve estatísticas públicas da plataforma.
//
// @Summary      Estatísticas da plataforma
// @Description  Devolve o número total de utilizadores e projetos registados
// @Tags         guest
// @Produce      json
// @Success      200  {object}  object{users=int,projects=int}
// @Router       /guest/stats [get]
func GetPlatformStats(c *gin.Context) {
	var userCount int64
	var projectCount int64

	database.DB.Model(&models.User{}).Count(&userCount)
	database.DB.Model(&models.Project{}).Count(&projectCount)

	c.JSON(http.StatusOK, gin.H{
		"users":    userCount,
		"projects": projectCount,
	})
}

// ClaimGuestSession — migra as preferências de uma sessão anónima para o perfil autenticado.
//
// @Summary      Migrar sessão anónima para perfil
// @Description  Associa as preferências de onboarding de uma sessão anónima ao utilizador autenticado. Apenas atualiza role se ainda não estiver definido no perfil. Apaga a sessão anónima após migração.
// @Tags         guest
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        input  body  object{token=string}  true  "Token da sessão anónima"
// @Success      200  {object}  models.User
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /users/me/claim-guest-session [post]
func ClaimGuestSession(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var input struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var session models.GuestSession
	if err := database.DB.Where("token = ? AND expires_at > ?", input.Token, time.Now()).
		First(&session).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Sessão anónima não encontrada ou expirada."})
		return
	}

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	// Only overwrite if the fields are still empty on the profile
	updated := false
	if user.Role == "" && session.Role != "" {
		user.Role = session.Role
		updated = true
	}

	if updated {
		database.DB.Model(&user).Updates(map[string]interface{}{
			"role": user.Role,
		})
	}

	// Delete the guest session so it cannot be claimed again
	database.DB.Delete(&session)

	c.JSON(http.StatusOK, gin.H{"message": "Sessão migrada com sucesso.", "user": user})
}
