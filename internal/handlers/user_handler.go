package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/middleware"
	"github.com/paiva/SkillBridge/Backend/internal/models"
	"github.com/paiva/SkillBridge/Backend/internal/storage"

	"github.com/gin-gonic/gin"
)

// getUserByIDOrSlug is a helper that resolves a user by ID (numeric) or slug.
// Returns the user and true if found, or nil and false if not found.
func getUserByIDOrSlug(idOrSlug string) (*models.User, bool) {
	var user models.User

	// Try to find by slug first
	err := database.DB.Where("slug = ?", idOrSlug).First(&user).Error
	if err == nil {
		return &user, true
	}

	// If not found by slug, try by numeric ID
	if err := database.DB.First(&user, idOrSlug).Error; err != nil {
		return nil, false
	}

	return &user, true
}

// extractGCSObjectName extracts the object path from a GCS URL
// Example: https://storage.googleapis.com/skillbridge-uploads/avatars/file.jpg -> avatars/file.jpg
func extractGCSObjectName(url string) string {
	if url == "" {
		return ""
	}
	// Handle GCS URL format
	if strings.Contains(url, "storage.googleapis.com/") {
		parts := strings.SplitN(url, "/", 5)
		if len(parts) >= 5 {
			return parts[4] // avatars/filename.jpg or projects/filename.jpg
		}
	}
	return ""
}

// RegisterUser - Cria perfil após registo no Firebase
//
// @Summary      Criar utilizador
// @Description  Cria um perfil de utilizador após autenticação Firebase
// @Tags         users
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        input  body  object{name=string,university=string,course=string,year=string,bio=string,role=string,guest_session_token=string}  true  "Dados do utilizador (email é lido automaticamente do token Firebase)"
// @Success      201  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      500  {object}  map[string]string
// @Router       /users/register [post]
func RegisterUser(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")
	firebaseEmail, _ := c.Get("email")
	emailStr := fmt.Sprintf("%v", firebaseEmail)

	var input struct {
		Name         string              `json:"name" binding:"required"`
		ContactLinks models.ContactLinks `json:"contact_links"`
		University   string              `json:"university"`
		Course       string              `json:"course"`
		Year         string              `json:"year"`
		Bio          string              `json:"bio"`
		Role         string              `json:"role"`
		// If the user filled the guest onboarding form, pass the token here
		// to have role automatically applied to the new profile.
		GuestSessionToken string `json:"guest_session_token"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Se já existe, devolve o perfil existente
	var existing models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&existing).Error; err == nil {
		c.JSON(http.StatusOK, gin.H{"message": "Utilizador já registado.", "user": existing})
		return
	}

	// Generate unique slug from name
	baseSlug := models.GenerateSlug(input.Name)
	slug := ensureUniqueUserSlug(baseSlug, 0)

	// Check if this is an OAuth account (Google, GitHub, Microsoft)
	// OAuth accounts are automatically verified
	emailVerified := false
	if provider, exists := c.Get("sign_in_provider"); exists {
		if providerStr, ok := provider.(string); ok {
			if providerStr == "google.com" || providerStr == "github.com" || providerStr == "microsoft.com" {
				emailVerified = true
				log.Printf("[register] Auto-verifying OAuth account (provider: %s) for UID=%s", providerStr, firebaseUID)
			}
		}
	}

	user := models.User{
		FirebaseUID:   firebaseUID,
		Name:          input.Name,
		Slug:          slug,
		Email:         emailStr,
		EmailVerified: emailVerified,
		University:    input.University,
		Course:        input.Course,
		Year:          input.Year,
		Bio:           input.Bio,
		Role:          input.Role,
		ContactLinks:  input.ContactLinks,
	}

	// Apply guest onboarding preferences if a session token was provided
	// These will override the direct input if present
	if input.GuestSessionToken != "" {
		var gs models.GuestSession
		if err := database.DB.Where("token = ? AND expires_at > ?", input.GuestSessionToken, time.Now()).
			First(&gs).Error; err == nil {
			if gs.Role != "" {
				user.Role = gs.Role
			}
			// Consume the session
			database.DB.Delete(&gs)
		}
	}

	if err := database.DB.Create(&user).Error; err != nil {
		log.Printf("[register] Erro ao criar utilizador firebase_uid=%s email=%s: %v", firebaseUID, emailStr, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao criar utilizador.", "detail": err.Error()})
		return
	}

	log.Printf("[register] Utilizador criado com sucesso: %s (%s)", user.Name, user.Email)
	c.JSON(http.StatusCreated, gin.H{"message": "Utilizador criado com sucesso.", "user": user})
}

// GetMyProfile - Devolve o perfil do utilizador autenticado
//
// @Summary      Obter perfil próprio
// @Description  Devolve o perfil do utilizador autenticado
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  models.User
// @Failure      404  {object}  map[string]string
// @Router       /users/me [get]
func GetMyProfile(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		firebaseEmail, _ := c.Get("email")
		emailStr := fmt.Sprintf("%v", firebaseEmail)

		// Account linking: email exists but under a different firebase_uid (e.g. email/password → Google)
		var existing models.User
		if emailStr != "" && emailStr != "<nil>" {
			if linkErr := database.DB.Where("email = ?", emailStr).First(&existing).Error; linkErr == nil {
				// Update the firebase_uid to the current one and return the existing profile
				database.DB.Model(&existing).Update("firebase_uid", firebaseUID)
				existing.FirebaseUID = firebaseUID
				c.JSON(http.StatusOK, existing)
				return
			}
		}

		// No profile found — frontend must complete onboarding
		c.JSON(http.StatusNotFound, gin.H{"error": "Perfil não encontrado. Por favor, completa o registo."})
		return
	}

	c.JSON(http.StatusOK, user)
}

// GetUserByID - Devolve o perfil público de um utilizador
//
// @Summary      Obter utilizador por ID ou slug
// @Description  Devolve o perfil público de um utilizador (busca por slug ou ID numérico)
// @Tags         users
// @Produce      json
// @Param        id   path      string  true  "ID numérico ou slug do utilizador"
// @Success      200  {object}  models.User
// @Failure      404  {object}  map[string]string
// @Router       /users/{id} [get]
func GetUserByID(c *gin.Context) {
	user, found := getUserByIDOrSlug(c.Param("id"))
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	// Omitir email do perfil público
	user.Email = ""
	c.JSON(http.StatusOK, user)
}

// UpdateProfile - Atualiza o perfil do utilizador autenticado
//
// @Summary      Atualizar perfil
// @Description  Atualiza os dados do perfil do utilizador autenticado
// @Tags         users
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        input  body  object{name=string,university=string,course=string,year=string,bio=string,avatar_url=string}  false  "Dados a atualizar"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /users/me [put]
func UpdateProfile(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var input struct {
		Name         string              `json:"name"`
		University   string              `json:"university"`
		Course       string              `json:"course"`
		Year         string              `json:"year"`
		Bio          string              `json:"bio"`
		AvatarURL    string              `json:"avatar_url"`
		Role         string              `json:"role"` // needs_help | helper
		ContactLinks models.ContactLinks `json:"contact_links"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// If avatar is being updated, delete old avatar from GCS
	if input.AvatarURL != "" && user.AvatarURL != "" && input.AvatarURL != user.AvatarURL {
		oldObjectName := extractGCSObjectName(user.AvatarURL)
		if oldObjectName != "" {
			if err := storage.DeleteFile(oldObjectName); err != nil {
				log.Printf("Warning: Failed to delete old avatar %s: %v", oldObjectName, err)
			}
		}
	}

	// If name is being updated, regenerate slug
	if input.Name != "" && input.Name != user.Name {
		baseSlug := models.GenerateSlug(input.Name)
		newSlug := ensureUniqueUserSlug(baseSlug, user.ID)
		user.Slug = newSlug
	}

	database.DB.Model(&user).Updates(input)
	database.DB.First(&user, user.ID)
	c.JSON(http.StatusOK, gin.H{"message": "Perfil atualizado.", "user": user})
}

// DeleteMyProfile - Remove a conta do utilizador autenticado
//
// @Summary      Eliminar conta própria
// @Description  Remove o utilizador e os seus dados associados
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /users/me [delete]
func DeleteMyProfile(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")
	log.Printf("DeleteMyProfile called for uid=%s", firebaseUID)
	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}
	// cleanup similar to admin path:
	// First delete all projects owned by this user (and their dependencies)
	var ownedProjects []models.Project
	database.DB.Where("owner_id = ?", user.ID).Find(&ownedProjects)
	for _, proj := range ownedProjects {
		// Delete project image from GCS
		if proj.ImageURL != "" {
			objectName := extractGCSObjectName(proj.ImageURL)
			if objectName != "" {
				if err := storage.DeleteFile(objectName); err != nil {
					log.Printf("Warning: Failed to delete project image %s: %v", objectName, err)
				}
			}
		}
		database.DB.Where("project_id = ?", proj.ID).Delete(&models.ProjectRole{})
		database.DB.Where("project_id = ?", proj.ID).Delete(&models.ProjectMember{})
		database.DB.Where("project_id = ?", proj.ID).Delete(&models.ProjectOwner{})
		database.DB.Delete(&proj)
	}

	database.DB.Where("user_id = ?", user.ID).Delete(&models.ProjectOwner{})
	database.DB.Where("user_id = ?", user.ID).Delete(&models.ProjectMember{})
	database.DB.Where("reviewer_id = ? OR reviewed_id = ?", user.ID, user.ID).Delete(&models.Review{})
	database.DB.Where("follower_id = ? OR following_id = ?", user.ID, user.ID).Delete(&models.Follow{})
	var convs []models.Conversation
	database.DB.Where("user_a_id = ? OR user_b_id = ?", user.ID, user.ID).Find(&convs)
	if len(convs) > 0 {
		ids := make([]uint, len(convs))
		for i, c2 := range convs {
			ids[i] = c2.ID
		}
		database.DB.Where("conversation_id IN ?", ids).Delete(&models.Message{})
		database.DB.Where("id IN ?", ids).Delete(&models.Conversation{})
	}
	database.DB.Where("user_id = ?", user.ID).Delete(&models.UserPublicKey{})
	database.DB.Where("user_id = ?", user.ID).Delete(&models.GuestSession{})

	// Delete user avatar from GCS
	if user.AvatarURL != "" {
		objectName := extractGCSObjectName(user.AvatarURL)
		if objectName != "" {
			if err := storage.DeleteFile(objectName); err != nil {
				log.Printf("Warning: Failed to delete user avatar %s: %v", objectName, err)
			}
		}
	}

	if err := middleware.DeleteUser(firebaseUID); err != nil {
		log.Printf("erro a eliminar utilizador firebase uid=%s: %v", firebaseUID, err)
	}
	if err := database.DB.Delete(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao eliminar utilizador."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Conta eliminada."})
}

// RequestPasswordReset - Envia email de redefinição de palavra-passe via Firebase
//
// @Summary      Pedir redefinição de palavra-passe
// @Description  Envia um email de reset de password usando Firebase Auth (sem Mailgun)
// @Tags         users
// @Accept       json
// @Produce      json
// @Param        input  body  object{email=string}  true  "Email do utilizador"
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  map[string]string
// @Router       /users/password-reset [post]
func RequestPasswordReset(c *gin.Context) {
	var input struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Encontrar utilizador (não revelar se existe ou não por segurança)
	var user models.User
	if err := database.DB.Where("email = ?", input.Email).First(&user).Error; err != nil {
		// Não informamos se o email não existe por segurança
		c.JSON(http.StatusOK, gin.H{"message": "Se o email existir, receberás um link de redefinição."})
		return
	}

	// Enviar email de reset usando Firebase (envia automaticamente)
	if err := middleware.SendPasswordResetEmail(input.Email); err != nil {
		log.Printf("[auth] Erro ao enviar email de reset para %s: %v", input.Email, err)
		// Não revelamos o erro específico ao cliente
		c.JSON(http.StatusOK, gin.H{"message": "Se o email existir, receberás um link de redefinição."})
		return
	}

	log.Printf("[auth] Email de reset enviado com sucesso para %s", input.Email)
	c.JSON(http.StatusOK, gin.H{"message": "Se o email existir, receberás um link de redefinição."})
}

// UpdateEmailVerification - Marca o email como verificado após confirmação via Firebase
//
// @Summary      Atualizar estado de verificação de email
// @Description  Marca o email do utilizador como verificado após verificação via Firebase
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  map[string]string
// @Router       /users/me/verify-email [post]
func UpdateEmailVerification(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	// Update email_verified to true
	user.EmailVerified = true
	if err := database.DB.Save(&user).Error; err != nil {
		log.Printf("[verify-email] Erro ao atualizar verificação para UID=%s: %v", firebaseUID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao atualizar verificação."})
		return
	}

	log.Printf("[verify-email] Email verificado para UID=%s", firebaseUID)
	c.JSON(http.StatusOK, gin.H{
		"message":        "Email verificado com sucesso.",
		"email_verified": true,
	})
}

// CheckEmailVerification - Verifica se o email está verificado
//
// @Summary      Verificar estado de verificação de email
// @Description  Retorna se o email do utilizador está verificado
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  map[string]string
// @Router       /users/me/email-verified [get]
func CheckEmailVerification(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"email_verified": user.EmailVerified,
	})
}
