package handlers

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/audit"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/middleware"
	"github.com/paiva/SkillBridge/Backend/internal/models"
	"github.com/paiva/SkillBridge/Backend/internal/storage"
)

// AdminCheckAccess - Lightweight endpoint to check if user is admin
// Only checks UID, returns info about IP whitelist and TOTP requirements
//
// @Summary      [Admin] Check admin access
// @Description  Verifies if the authenticated user is an admin (UID only check)
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]string
// @Router       /admin/check-access [get]
func AdminCheckAccess(c *gin.Context) {
	uid := c.GetString("firebase_uid")

	// Check UID - is this user in the admin list?
	if !config.IsAdmin(uid) {
		audit.LogAction(c, audit.ActionUnauthorized, "UID %s not in admin list", uid)
		c.JSON(http.StatusForbidden, gin.H{
			"error":    "Not an administrator. Your Firebase UID is not in the admin whitelist.",
			"is_admin": false,
			"uid":      uid,
		})
		return
	}

	// User is admin - now check IP whitelist if configured
	clientIP := getClientIP(c)
	ipWhitelistEnabled := len(config.AppConfig.AdminAllowedIPs) > 0
	ipAllowed := true

	if ipWhitelistEnabled {
		ipAllowed = isIPInWhitelist(clientIP, config.AppConfig.AdminAllowedIPs)
		if !ipAllowed {
			log.Printf("[Admin Check] UID=%s is admin but IP=%s not in whitelist %v", uid, clientIP, config.AppConfig.AdminAllowedIPs)
			audit.LogAction(c, audit.ActionUnauthorized, "Admin UID=%s with non-whitelisted IP=%s", uid, clientIP)
			c.JSON(http.StatusForbidden, gin.H{
				"error":                "Your IP address is not authorized for admin access.",
				"is_admin":             true,
				"uid":                  uid,
				"ip_whitelist_enabled": true,
				"your_ip":              clientIP,
				"allowed_ips":          config.AppConfig.AdminAllowedIPs,
			})
			return
		}
	}

	log.Printf("[Admin Check] Access granted for UID=%s IP=%s", uid, clientIP)
	audit.LogAction(c, audit.ActionLogin, "Admin check passed for UID=%s IP=%s", uid, clientIP)

	// All checks passed
	c.JSON(http.StatusOK, gin.H{
		"is_admin":             true,
		"uid":                  uid,
		"ip_whitelist_enabled": ipWhitelistEnabled,
		"your_ip":              clientIP,
		"ip_allowed":           ipAllowed,
	})
}

// Helper function to check IP against whitelist
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

func isIPInWhitelist(ip string, whitelist []string) bool {
	if len(whitelist) == 0 {
		return true // No whitelist = all IPs allowed
	}

	for _, allowedIP := range whitelist {
		if allowedIP == ip {
			return true
		}
	}
	return false
}

// AdminListUsers - Lista todos os utilizadores (admin)
//
// @Summary      [Admin] Listar utilizadores
// @Description  Devolve todos os utilizadores registados, incluindo email
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}   models.User
// @Router       /admin/users [get]
func AdminListUsers(c *gin.Context) {
	var users []models.User
	database.DB.Order("created_at DESC").Find(&users)
	c.JSON(http.StatusOK, users)
}

// AdminGetUser - Detalhes completos de um utilizador (admin)
//
// @Summary      [Admin] Obter utilizador
// @Description  Devolve o perfil completo de um utilizador, incluindo email e projetos
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int  true  "ID do utilizador"
// @Success      200  {object}  models.User
// @Failure      404  {object}  map[string]string
// @Router       /admin/users/{id} [get]
func AdminGetUser(c *gin.Context) {
	var user models.User
	if err := database.DB.Preload("OwnedProjects").
		First(&user, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}
	c.JSON(http.StatusOK, user)
}

// AdminDeleteUser - Elimina um utilizador (admin)
//
// @Summary      [Admin] Eliminar utilizador
// @Description  Remove permanentemente um utilizador e os seus dados
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int  true  "ID do utilizador"
// @Success      200  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /admin/users/{id} [delete]
func AdminDeleteUser(c *gin.Context) {
	var user models.User
	if err := database.DB.First(&user, c.Param("id")).Error; err != nil {
		audit.LogFailure(c, audit.ActionUserDelete, "User not found")
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	userID := user.ID
	userName := user.Name
	userEmail := user.Email

	// perform the same cleanup as DeleteMyProfile
	// Delete owned projects and their images from GCS
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
	database.DB.Where("user_id = ?", user.ID).Delete(&models.PushDeviceToken{})
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

	// delete firebase record if possible
	firebaseUID := user.FirebaseUID
	if firebaseUID != "" {
		if err := middleware.DeleteUser(firebaseUID); err != nil {
			log.Printf("erro a eliminar utilizador firebase uid=%s: %v", firebaseUID, err)
		}
	}
	database.DB.Delete(&user)

	audit.LogAction(c, audit.ActionUserDelete,
		"Deleted user ID=%d Name=%s Email=%s", userID, userName, userEmail)

	c.JSON(http.StatusOK, gin.H{"message": "Utilizador eliminado com sucesso."})
}

// AdminListProjects - Lista todos os projetos (admin)
//
// @Summary      [Admin] Listar projetos
// @Description  Devolve todos os projetos independentemente do estado
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}   models.Project
// @Router       /admin/projects [get]
func AdminListProjects(c *gin.Context) {
	var projects []models.Project
	database.DB.Preload("Owner").Preload("Roles").Preload("Members.User").Order("created_at DESC").Find(&projects)
	c.JSON(http.StatusOK, projects)
}

// AdminDeleteProject - Elimina um projeto (admin)
//
// @Summary      [Admin] Eliminar projeto
// @Description  Remove permanentemente um projeto
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int  true  "ID do projeto"
// @Success      200  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /admin/projects/{id} [delete]
func AdminDeleteProject(c *gin.Context) {
	var project models.Project
	if err := database.DB.First(&project, c.Param("id")).Error; err != nil {
		audit.LogFailure(c, audit.ActionProjectDelete, "Project not found")
		c.JSON(http.StatusNotFound, gin.H{"error": "Projeto não encontrado."})
		return
	}

	projectID := project.ID
	projectTitle := project.Title

	// Delete child records first to avoid FK constraint violations
	database.DB.Where("project_id = ?", project.ID).Delete(&models.ProjectOwner{})
	database.DB.Where("project_id = ?", project.ID).Delete(&models.ProjectRole{})
	database.DB.Where("project_id = ?", project.ID).Delete(&models.ProjectMember{})
	database.DB.Where("project_id = ?", project.ID).Delete(&models.Review{})
	if err := database.DB.Delete(&project).Error; err != nil {
		audit.LogFailure(c, audit.ActionProjectDelete, "Database error")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao eliminar projeto."})
		return
	}

	audit.LogAction(c, audit.ActionProjectDelete,
		"Deleted project ID=%d Title=%s", projectID, projectTitle)

	c.JSON(http.StatusOK, gin.H{"message": "Projeto eliminado."})
}

// AdminListDonations - Lista todos os donativos (admin)
//
// @Summary      [Admin] Listar donativos
// @Description  Devolve todos os donativos com detalhes completos
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}   models.Donation
// @Router       /admin/donations [get]
func AdminListDonations(c *gin.Context) {
	var donations []models.Donation
	database.DB.Order("created_at DESC").Find(&donations)
	c.JSON(http.StatusOK, donations)
}

// AdminListReviews - Lista avaliações pendentes (admin)
//
// @Summary      [Admin] Listar avaliações pendentes
// @Description  Devolve todas as avaliações com status=pending para moderação
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        status  query  string  false  "Filtrar por estado: pending (default), approved, rejected, all"
// @Success      200  {array}   models.Review
// @Router       /admin/reviews [get]
func AdminListReviews(c *gin.Context) {
	status := c.DefaultQuery("status", "pending")
	var reviews []models.Review
	q := database.DB.Order("created_at DESC")
	if status != "all" {
		q = q.Where("status = ?", status)
	}
	q.Find(&reviews)
	c.JSON(http.StatusOK, reviews)
}

// AdminDecideReview - Aprova ou rejeita uma avaliação (admin)
//
// @Summary      [Admin] Aprovar/Rejeitar avaliação
// @Description  Muda o estado de uma avaliação para 'approved' ou 'rejected'
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id     path  int     true  "ID da avaliação"
// @Param        input  body  object{status=string}  true  "Novo estado: approved | rejected"
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /admin/reviews/{id} [put]
func AdminDecideReview(c *gin.Context) {
	var review models.Review
	if err := database.DB.First(&review, c.Param("id")).Error; err != nil {
		audit.LogFailure(c, audit.ActionReviewApprove, "Review not found")
		c.JSON(http.StatusNotFound, gin.H{"error": "Avaliação não encontrada."})
		return
	}

	var input struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		audit.LogFailure(c, audit.ActionReviewApprove, "Invalid input")
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if input.Status != "approved" && input.Status != "rejected" {
		audit.LogFailure(c, audit.ActionReviewApprove, "Invalid status: "+input.Status)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Estado inválido. Use 'approved' ou 'rejected'."})
		return
	}

	database.DB.Model(&review).Update("status", input.Status)

	if input.Status == "approved" {
		audit.LogAction(c, audit.ActionReviewApprove, "Approved review ID=%d", review.ID)
	} else {
		audit.LogAction(c, audit.ActionReviewReject, "Rejected review ID=%d", review.ID)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Avaliação " + input.Status + ".", "review_id": review.ID})
}

// AdminDeleteReview - Elimina uma avaliação (admin)
//
// @Summary      [Admin] Eliminar avaliação
// @Description  Remove permanentemente uma avaliação
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int  true  "ID da avaliação"
// @Success      200  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /admin/reviews/{id} [delete]
func AdminDeleteReview(c *gin.Context) {
	var review models.Review
	if err := database.DB.First(&review, c.Param("id")).Error; err != nil {
		audit.LogFailure(c, audit.ActionReviewDelete, "Review not found")
		c.JSON(http.StatusNotFound, gin.H{"error": "Avaliação não encontrada."})
		return
	}

	reviewID := review.ID

	database.DB.Delete(&review)

	audit.LogAction(c, audit.ActionReviewDelete, "Deleted review ID=%d", reviewID)

	c.JSON(http.StatusOK, gin.H{"message": "Avaliação eliminada."})
}

// AdminGetAuditLogs - Lista registos de auditoria (admin)
//
// @Summary      [Admin] Listar audit logs
// @Description  Devolve registos de auditoria de ações administrativas
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Param        uid     query  string  false  "Filtrar por Firebase UID"
// @Param        action  query  string  false  "Filtrar por tipo de ação"
// @Param        limit   query  int     false  "Número máximo de registos (default: 100)"
// @Success      200  {array}   models.AuditLog
// @Router       /admin/audit-logs [get]
func AdminGetAuditLogs(c *gin.Context) {
	uid := c.Query("uid")
	action := c.Query("action")
	limit := 100
	if l := c.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 1000 {
			limit = parsed
		}
	}

	var logs []models.AuditLog
	query := database.DB.Order("timestamp DESC")

	if uid != "" {
		query = query.Where("firebase_uid = ?", uid)
	}
	if action != "" {
		query = query.Where("action = ?", action)
	}

	query = query.Limit(limit)
	query.Find(&logs)

	c.JSON(http.StatusOK, logs)
}
