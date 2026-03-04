package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/models"
	"github.com/paiva/SkillBridge/Backend/internal/middleware"
	"github.com/paiva/SkillBridge/Backend/internal/storage"
)

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
	database.DB.Preload("Skills.Skill").Order("created_at DESC").Find(&users)
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
	if err := database.DB.Preload("Skills.Skill").Preload("OwnedProjects").
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
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}
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
		for i, c2 := range convs { ids[i] = c2.ID }
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
	
	// delete firebase record if possible
	firebaseUID := user.FirebaseUID
  if firebaseUID != "" {
    if err := middleware.DeleteUser(firebaseUID); err != nil {
      log.Printf("erro a eliminar utilizador firebase uid=%s: %v", firebaseUID, err)
    }
  }
	database.DB.Delete(&user)
	c.JSON(http.StatusOK, gin.H{"message": "Utilizador eliminado."})
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
	database.DB.Preload("Owner").Preload("Members").Order("created_at DESC").Find(&projects)
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
		c.JSON(http.StatusNotFound, gin.H{"error": "Projeto não encontrado."})
		return
	}
	// Delete child records first to avoid FK constraint violations
	database.DB.Where("project_id = ?", project.ID).Delete(&models.ProjectOwner{})
	database.DB.Where("project_id = ?", project.ID).Delete(&models.ProjectRole{})
	database.DB.Where("project_id = ?", project.ID).Delete(&models.ProjectMember{})
	database.DB.Where("project_id = ?", project.ID).Delete(&models.Review{})
	if err := database.DB.Delete(&project).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao eliminar projeto."})
		return
	}
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
		c.JSON(http.StatusNotFound, gin.H{"error": "Avaliação não encontrada."})
		return
	}

	var input struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if input.Status != "approved" && input.Status != "rejected" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Estado inválido. Use 'approved' ou 'rejected'."})
		return
	}

	database.DB.Model(&review).Update("status", input.Status)
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
		c.JSON(http.StatusNotFound, gin.H{"error": "Avaliação não encontrada."})
		return
	}
	database.DB.Delete(&review)
	c.JSON(http.StatusOK, gin.H{"message": "Avaliação eliminada."})
}
