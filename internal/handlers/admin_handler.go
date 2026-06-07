package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	gcs "cloud.google.com/go/storage"
	"google.golang.org/api/iterator"
	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/audit"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/email"
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

// AdminUpdateUser - Atualiza os dados de um utilizador (admin)
//
// @Summary      [Admin] Atualizar utilizador
// @Description  Atualiza os dados do utilizador
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int  true  "ID do utilizador"
// @Success      200  {object}  map[string]interface{}
// @Router       /admin/users/{id} [put]
func AdminUpdateUser(c *gin.Context) {
	var user models.User
	if err := database.DB.First(&user, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var input struct {
		Name       string            `json:"name"`
		Email      string            `json:"email"`
		University string            `json:"university"`
		Course     string            `json:"course"`
		Year       string            `json:"year"`
		Role       string            `json:"role"`
		Bio        string            `json:"bio"`
		AvatarURL  *string           `json:"avatar_url"`
		Skills     models.StringList `json:"skills"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if input.AvatarURL != nil {
		newAvatarURL := *input.AvatarURL
		if user.AvatarURL != "" && newAvatarURL != user.AvatarURL {
			oldObjectName := extractGCSObjectName(user.AvatarURL)
			if oldObjectName != "" {
				if err := storage.DeleteFile(oldObjectName); err != nil {
					log.Printf("Warning: Failed to delete old avatar %s: %v", oldObjectName, err)
				}
			}
		}
		user.AvatarURL = newAvatarURL
	}

	if input.Name != "" {
		user.Name = input.Name
	}
	if input.Email != "" {
		user.Email = input.Email
	}
	if input.University != "" {
		user.University = input.University
	}
	if input.Course != "" {
		user.Course = input.Course
	}
	if input.Year != "" {
		user.Year = input.Year
	}
	if input.Role != "" {
		user.Role = input.Role
	}
	if input.Bio != "" {
		user.Bio = input.Bio
	}
	if input.Skills != nil {
		user.Skills = input.Skills
	}

	database.DB.Save(&user)
	audit.LogAction(c, audit.ActionUserUpdate, "Updated user ID=%d Name=%s", user.ID, user.Name)

	c.JSON(http.StatusOK, gin.H{"message": "Utilizador atualizado.", "user": user})
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

// AdminUpdateProject - Atualiza um projeto e as suas vagas (admin)
//
// @Summary      [Admin] Atualizar projeto
// @Description  Atualiza um projeto e vagas associadas
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int  true  "ID do projeto"
// @Success      200  {object}  map[string]interface{}
// @Router       /admin/projects/{id} [put]
func AdminUpdateProject(c *gin.Context) {
	var project models.Project
	if err := database.DB.Preload("Roles").First(&project, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Projeto não encontrado."})
		return
	}

	var input struct {
		Title       string             `json:"title"`
		Description string             `json:"description"`
		Status      string             `json:"status"`
		ImageURL    *string            `json:"image_url"`
		Roles       []projectRoleInput `json:"roles"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if input.ImageURL != nil {
		newImageURL := *input.ImageURL
		if project.ImageURL != "" && newImageURL != project.ImageURL {
			oldObjectName := extractGCSObjectName(project.ImageURL)
			if oldObjectName != "" {
				if err := storage.DeleteFile(oldObjectName); err != nil {
					log.Printf("Warning: Failed to delete old project image %s: %v", oldObjectName, err)
				}
			}
		}
		project.ImageURL = newImageURL
	}

	if input.Title != "" {
		project.Title = input.Title
	}
	if input.Description != "" {
		project.Description = input.Description
	}
	if input.Status != "" {
		project.Status = input.Status
	}
	database.DB.Save(&project)

	if input.Roles != nil {
		roles, err := prepareProjectRoles(input.Roles, false)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		var existingRoles []models.ProjectRole
		database.DB.Where("project_id = ?", project.ID).Find(&existingRoles)

		existingRoleMap := make(map[uint]models.ProjectRole)
		for _, r := range existingRoles {
			existingRoleMap[r.ID] = r
		}

		for _, role := range roles {
			if role.ID != 0 && existingRoleMap[role.ID].ID != 0 {
				database.DB.Model(&models.ProjectRole{}).Where("id = ? AND project_id = ?", role.ID, project.ID).Updates(map[string]interface{}{
					"title":       role.Title,
					"skill_names": role.SkillNames,
					"description": role.Description,
					"spots":       role.Spots,
				})
				delete(existingRoleMap, role.ID)
			} else {
				role.ID = 0
				role.ProjectID = project.ID
				database.DB.Create(&role)
			}
		}

		if len(existingRoleMap) > 0 {
			var idsToDelete []uint
			for id := range existingRoleMap {
				idsToDelete = append(idsToDelete, id)
			}
			database.DB.Where("role_id IN ?", idsToDelete).Delete(&models.ProjectMember{})
			database.DB.Where("id IN ?", idsToDelete).Delete(&models.ProjectRole{})
		}
	}

	database.DB.Preload("Roles").Preload("Owner").Preload("Members.User").First(&project, project.ID)
	audit.LogAction(c, audit.ActionProjectUpdate, "Updated project ID=%d Title=%s", project.ID, project.Title)

	c.JSON(http.StatusOK, gin.H{"message": "Projeto atualizado.", "project": project})
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

		// Send approval notification email to the reviewer (best-effort, non-blocking)
		go func() {
			var reviewer models.User
			if err := database.DB.First(&reviewer, review.ReviewerID).Error; err == nil {
				var reviewed models.User
				if err := database.DB.First(&reviewed, review.ReviewedID).Error; err == nil {
					profileURL := fmt.Sprintf("%s/users/%s", config.AppConfig.FrontendURL, reviewed.Slug)
					if err := email.SendReviewApproved(reviewer.Email, reviewer.Name, reviewed.Name, review.Rating, profileURL); err != nil {
						log.Printf("[email] Erro ao enviar email de review aprovada para %s: %v", reviewer.Email, err)
					}
				}
			}
		}()
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

// AdminCleanUnusedImages - Limpa imagens no GCS que não estão a ser utilizadas
//
// @Summary      [Admin] Limpar imagens não utilizadas
// @Description  Compara as imagens ativas da DB com o GCS e remove órfãs
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]interface{}
// @Router       /admin/clean-unused-images [post]
func AdminCleanUnusedImages(c *gin.Context) {
	if storage.GCSClient == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "GCS client not initialized"})
		return
	}

	// 1. Obter todos os avatares ativos da DB
	var userAvatars []string
	database.DB.Model(&models.User{}).Where("avatar_url != '' AND avatar_url IS NOT NULL").Pluck("avatar_url", &userAvatars)

	// 2. Obter todas as imagens de projeto ativas da DB
	var projectImages []string
	database.DB.Model(&models.Project{}).Where("image_url != '' AND image_url IS NOT NULL").Pluck("image_url", &projectImages)

	// 3. Mapear nomes de objetos ativos
	activeObjects := make(map[string]bool)
	for _, url := range userAvatars {
		objName := extractGCSObjectName(url)
		if objName != "" {
			activeObjects[objName] = true
		}
	}
	for _, url := range projectImages {
		objName := extractGCSObjectName(url)
		if objName != "" {
			activeObjects[objName] = true
		}
	}

	ctx := context.Background()
	bucketName := config.AppConfig.GCSBucketName
	bucket := storage.GCSClient.Bucket(bucketName)

	var deleted []string

	// 4. Listar e apagar avatares órfãos no GCS
	itAvatars := bucket.Objects(ctx, &gcs.Query{Prefix: "avatars/"})
	for {
		attrs, err := itAvatars.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Error iterating avatars GCS: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao listar avatares do GCS", "details": err.Error()})
			return
		}
		// Ignorar o próprio prefixo como diretório virtual (se houver)
		if attrs.Name == "avatars/" {
			continue
		}
		if !activeObjects[attrs.Name] {
			if err := bucket.Object(attrs.Name).Delete(ctx); err != nil {
				log.Printf("Warning: failed to delete unused avatar %s: %v", attrs.Name, err)
			} else {
				deleted = append(deleted, attrs.Name)
			}
		}
	}

	// 5. Listar e apagar imagens de projeto órfãs no GCS
	itProjects := bucket.Objects(ctx, &gcs.Query{Prefix: "projects/"})
	for {
		attrs, err := itProjects.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Error iterating projects GCS: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao listar imagens de projeto do GCS", "details": err.Error()})
			return
		}
		// Ignorar prefixo
		if attrs.Name == "projects/" {
			continue
		}
		if !activeObjects[attrs.Name] {
			if err := bucket.Object(attrs.Name).Delete(ctx); err != nil {
				log.Printf("Warning: failed to delete unused project image %s: %v", attrs.Name, err)
			} else {
				deleted = append(deleted, attrs.Name)
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Limpeza concluída com sucesso.",
		"deleted_count": len(deleted),
		"deleted_files": deleted,
	})
}

// AdminSendMarketingEmail - Envia email de marketing customizado (admin)
//
// @Summary      [Admin] Enviar email de marketing
// @Description  Envia um email HTML customizado para todos os utilizadores ou para uma seleção
// @Tags         admin
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        input  body  object  true  "Payload com subject, html e lista opcional de user IDs"
// @Success      200  {object}  map[string]interface{}
// @Router       /admin/send-marketing-email [post]
func AdminSendMarketingEmail(c *gin.Context) {
	var input struct {
		Subject string `json:"subject" binding:"required"`
		HTML    string `json:"html" binding:"required"`
		UserIDs []uint `json:"user_ids"` // empty = todos
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Recolher destinatários
	var users []models.User
	if len(input.UserIDs) > 0 {
		database.DB.Where("id IN ?", input.UserIDs).Find(&users)
	} else {
		database.DB.Where("email != ''").Find(&users)
	}

	if len(users) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "Nenhum destinatário encontrado.", "sent": 0, "failed": 0})
		return
	}

	audit.LogAction(c, audit.ActionUserUpdate,
		"Marketing email sent: subject=%q recipients=%d", input.Subject, len(users))

	var sent, failed int
	var failedEmails []string

	log.Printf("[marketing email] A iniciar envio sequencial para %d utilizadores com intervalo de 5 segundos...", len(users))
	for i, u := range users {
		if u.Email == "" {
			continue
		}

		// Adiciona o delay de 5 segundos sugerido entre envios (exceto no primeiro)
		if i > 0 {
			time.Sleep(5 * time.Second)
		}

		log.Printf("[marketing email] [%d/%d] A enviar para %s...", i+1, len(users), u.Email)
		err := email.SendCustomMarketing(input.Subject, u.Email, input.HTML)
		if err != nil {
			failed++
			failedEmails = append(failedEmails, u.Email)
			log.Printf("[marketing email] Falha para %s: %v", u.Email, err)
		} else {
			sent++
			log.Printf("[marketing email] [%d/%d] Enviado com sucesso para %s", i+1, len(users), u.Email)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":       fmt.Sprintf("Email enviado para %d destinatário(s).", sent),
		"sent":          sent,
		"failed":        failed,
		"failed_emails": failedEmails,
	})
}

// AdminGetUniversityStats - Top 10 universidades com mais utentes
//
// @Summary      [Admin] Estatísticas por universidade
// @Description  Devolve as 10 universidades com mais utilizadores, com contagem por role
// @Tags         admin
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}  map[string]interface{}
// @Router       /admin/university-stats [get]
func AdminGetUniversityStats(c *gin.Context) {
	type UniversityRow struct {
		University string `json:"university"`
		Total      int64  `json:"total"`
		Helpers    int64  `json:"helpers"`
		NeedsHelp  int64  `json:"needs_help"`
		Both       int64  `json:"both"`
	}

	// Aggregate total users per university (non-empty)
	type rawRow struct {
		University string
		Total      int64
	}
	var rows []rawRow
	database.DB.Model(&models.User{}).
		Select("university, COUNT(*) AS total").
		Where("university != '' AND university IS NOT NULL").
		Group("university").
		Order("total DESC").
		Limit(10).
		Scan(&rows)

	result := make([]UniversityRow, 0, len(rows))
	for _, r := range rows {
		var helpers, needsHelp, both int64
		database.DB.Model(&models.User{}).
			Where("university = ? AND role = ?", r.University, "helper").
			Count(&helpers)
		database.DB.Model(&models.User{}).
			Where("university = ? AND role = ?", r.University, "needs_help").
			Count(&needsHelp)
		database.DB.Model(&models.User{}).
			Where("university = ? AND role = ?", r.University, "both").
			Count(&both)

		result = append(result, UniversityRow{
			University: r.University,
			Total:      r.Total,
			Helpers:    helpers,
			NeedsHelp:  needsHelp,
			Both:       both,
		})
	}

	c.JSON(http.StatusOK, result)
}

// ── Admin Vacancy Management ──────────────────────────────

// AdminListVacancies lists all vacancies.
// GET /api/admin/vacancies
func AdminListVacancies(c *gin.Context) {
	var vacancies []models.Vacancy
	if err := database.DB.Preload("Recruiter").Order("published_at DESC").Find(&vacancies).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao listar vagas."})
		return
	}
	c.JSON(http.StatusOK, vacancies)
}

// AdminCreateVacancy creates a vacancy manually.
// POST /api/admin/vacancies
func AdminCreateVacancy(c *gin.Context) {
	var input struct {
		CompanyName    string   `json:"company_name"`
		CompanyURL     string   `json:"company_url"`
		LogoURL        string   `json:"logo_url"`
		RecruiterID    string   `json:"recruiter_id"`
		Title          string   `json:"title" binding:"required"`
		Type           string   `json:"type" binding:"required"`
		Tags           []string `json:"tags"`
		Description    string   `json:"description" binding:"required"`
		ApplicationURL string   `json:"application_url" binding:"required"`
		Region         string   `json:"region"`
		WorkMode       string   `json:"work_mode"`
		EmploymentType string   `json:"employment_type"`
		ExpiresAt      *time.Time `json:"expires_at"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados inválidos."})
		return
	}

	var recruiter models.Recruiter
	if input.RecruiterID != "" {
		if err := database.DB.Where("id = ?", input.RecruiterID).First(&recruiter).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Recrutador não encontrado."})
			return
		}
	} else if input.CompanyName != "" {
		err := database.DB.Where("LOWER(company_name) = ?", strings.ToLower(strings.TrimSpace(input.CompanyName))).First(&recruiter).Error
		if err != nil {
			recruiter = models.Recruiter{
				FullName:           "Admin Imported",
				CompanyName:        strings.TrimSpace(input.CompanyName),
				Email:              fmt.Sprintf("dummy_%d@dummy.skillbridge.pt", time.Now().UnixNano()),
				CompanyURL:         input.CompanyURL,
				LogoURL:            input.LogoURL,
				Status:             "approved",
			}
			if err := database.DB.Create(&recruiter).Error; err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao criar empresa dummy."})
				return
			}
		}
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Forneça RecruiterID ou CompanyName."})
		return
	}

	expiresAt := time.Now().AddDate(0, 1, 0)
	if input.ExpiresAt != nil {
		expiresAt = *input.ExpiresAt
	}

	vacancy := models.Vacancy{
		RecruiterID:    recruiter.ID,
		Title:          input.Title,
		Type:           input.Type,
		Tags:           input.Tags,
		Description:    input.Description,
		ApplicationURL: input.ApplicationURL,
		Region:         input.Region,
		WorkMode:       input.WorkMode,
		EmploymentType: input.EmploymentType,
		ExpiresAt:      expiresAt,
		Status:         "active",
		PublishedAt:    time.Now(),
	}

	if err := database.DB.Create(&vacancy).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao criar vaga."})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Vaga criada.", "vacancy": vacancy})
}

// AdminUpdateVacancy updates a vacancy.
// PUT /api/admin/vacancies/:id
func AdminUpdateVacancy(c *gin.Context) {
	var vacancy models.Vacancy
	if err := database.DB.First(&vacancy, "id = ?", c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Vaga não encontrada."})
		return
	}

	var input struct {
		Title          string   `json:"title"`
		Type           string   `json:"type"`
		Tags           []string `json:"tags"`
		Description    string   `json:"description"`
		ApplicationURL string   `json:"application_url"`
		Region         string   `json:"region"`
		WorkMode       string   `json:"work_mode"`
		EmploymentType string   `json:"employment_type"`
		Status         string   `json:"status"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados inválidos."})
		return
	}

	updates := map[string]interface{}{}
	if input.Title != "" { updates["title"] = input.Title }
	if input.Type != "" { updates["type"] = input.Type }
	if input.Tags != nil { updates["tags"] = models.StringList(input.Tags) }
	if input.Description != "" { updates["description"] = input.Description }
	if input.ApplicationURL != "" { updates["application_url"] = input.ApplicationURL }
	if input.Region != "" { updates["region"] = input.Region }
	if input.WorkMode != "" { updates["work_mode"] = input.WorkMode }
	if input.EmploymentType != "" { updates["employment_type"] = input.EmploymentType }
	if input.Status != "" { updates["status"] = input.Status }

	if len(updates) > 0 {
		database.DB.Model(&vacancy).Updates(updates)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Vaga atualizada."})
}

// AdminDeleteVacancy deletes a vacancy.
// DELETE /api/admin/vacancies/:id
func AdminDeleteVacancy(c *gin.Context) {
	var vacancy models.Vacancy
	if err := database.DB.First(&vacancy, "id = ?", c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Vaga não encontrada."})
		return
	}

	if err := database.DB.Delete(&vacancy).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao eliminar vaga."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Vaga eliminada."})
}

// extractSkillsFromText parses a description and finds any matching valid skills
func extractSkillsFromText(text string) []string {
	if text == "" {
		return []string{}
	}
	textLower := strings.ToLower(text)
	var found []string
	for _, skill := range config.Skills {
		// we check if the lowercase skill exists in the lowercase text
		if strings.Contains(textLower, strings.ToLower(skill)) {
			found = append(found, skill)
		}
	}
	return found
}

// resolveRecruiterByCompanyName finds a recruiter by name or creates a dummy one
func resolveRecruiterByCompanyName(companyName string) string {
	if companyName == "" {
		return ""
	}
	var rec models.Recruiter
	if err := database.DB.Where("LOWER(company_name) = LOWER(?)", companyName).First(&rec).Error; err == nil {
		return rec.ID
	}
	
	// Create dummy recruiter
	domainSafe := strings.ToLower(strings.ReplaceAll(companyName, " ", ""))
	rec = models.Recruiter{
		FullName:    "Admin Auto-Scraper",
		CompanyName: companyName,
		Email:       domainSafe + "@auto-scraped.com",
		Status:      "approved",
		LogoURL:     "https://www.google.com/s2/favicons?domain=" + domainSafe + ".com&sz=128",
	}
	if err := database.DB.Create(&rec).Error; err == nil {
		return rec.ID
	}
	return ""
}

// AdminBulkUpdateVacancies allows bulk editing/creating vacancies via JSON
// PUT /api/admin/vacancies/bulk
func AdminBulkUpdateVacancies(c *gin.Context) {
	var vacancies []models.Vacancy
	if err := c.ShouldBindJSON(&vacancies); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Formato JSON inválido."})
		return
	}

	var updated, created int
	for i := range vacancies {
		v := &vacancies[i]

		// If CompanyName is provided, resolve it and OVERRIDE any provided RecruiterID
		if v.CompanyName != "" {
			resolvedID := resolveRecruiterByCompanyName(v.CompanyName)
			if resolvedID != "" {
				v.RecruiterID = resolvedID
			}
		}

		// Fallback check: we must have a RecruiterID at this point, or it will fail foreign key constraint
		if v.RecruiterID == "" {
			continue // skip invalid vacancies silently or we could log it
		}

		// Auto-extract skills if missing
		if len(v.Tags) == 0 && v.Description != "" {
			v.Tags = extractSkillsFromText(v.Description)
		}

		if v.ID == "" {
			// CREATE (new vacancy)
			if v.Status == "" {
				v.Status = "active"
			}
			if err := database.DB.Create(v).Error; err == nil {
				created++
			}
		} else {
			// UPDATE
			var existing models.Vacancy
			if err := database.DB.Where("id = ?", v.ID).First(&existing).Error; err == nil {
				if err := database.DB.Model(&existing).Updates(v).Error; err == nil {
					updated++
				}
			} else {
				// ID provided but not found, so we create it with that ID
				if v.Status == "" {
					v.Status = "active"
				}
				if err := database.DB.Create(v).Error; err == nil {
					created++
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("%d criadas, %d atualizadas com sucesso via Bulk JSON.", created, updated)})
}
