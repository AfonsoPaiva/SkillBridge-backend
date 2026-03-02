package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/models"
)

// CreateReview - Cria uma avaliação de um utilizador
//
// @Summary      Criar avaliação
// @Description  O utilizador autenticado avalia outro utilizador. A avaliação fica pendente até aprovação do administrador.
// @Tags         reviews
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        input  body  object{reviewed_id=integer,project_id=integer,rating=integer,comment=string}  true  "Dados da avaliação (rating: 1-5, project_id opcional)"
// @Success      201  {object}  models.Review
// @Failure      400  {object}  map[string]string
// @Failure      409  {object}  map[string]string
// @Router       /reviews [post]
func CreateReview(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var reviewer models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&reviewer).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var input struct {
		ReviewedID uint   `json:"reviewed_id" binding:"required"`
		ProjectID  *uint  `json:"project_id"` // opcional
		Rating     int    `json:"rating" binding:"required,min=1,max=5"`
		Comment    string `json:"comment"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if reviewer.ID == input.ReviewedID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Não pode avaliar-se a si mesmo."})
		return
	}

	// Verificar que o utilizador avaliado existe
	var reviewed models.User
	if err := database.DB.First(&reviewed, input.ReviewedID).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Utilizador avaliado não encontrado."})
		return
	}

	// Verificar que não existe avaliação duplicada do mesmo avaliador para o mesmo avaliado
	var existing models.Review
	if database.DB.Where("reviewer_id = ? AND reviewed_id = ?",
		reviewer.ID, input.ReviewedID).First(&existing).Error == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Já avaliaste este utilizador."})
		return
	}

	review := models.Review{
		ReviewerID: reviewer.ID,
		ReviewedID: input.ReviewedID,
		ProjectID:  input.ProjectID,
		Rating:     input.Rating,
		Comment:    input.Comment,
		Status:     "pending",
	}
	database.DB.Create(&review)
	c.JSON(http.StatusCreated, gin.H{
		"message": "Avaliação submetida. Ficará visível após aprovação pelo administrador.",
		"review":  review,
	})
}

// GetUserReviews - Lista as avaliações recebidas por um utilizador
//
// @Summary      Avaliações de um utilizador
// @Description  Devolve todas as avaliações recebidas por um utilizador, com média de rating
// @Tags         reviews
// @Produce      json
// @Param        id  path  int  true  "ID do utilizador"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  map[string]string
// @Router       /users/{id}/reviews [get]
func GetUserReviews(c *gin.Context) {
	var user models.User
	if err := database.DB.First(&user, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var reviews []models.Review
	database.DB.Preload("Reviewer").Preload("Project").Where("reviewed_id = ? AND status = 'approved'", user.ID).Order("created_at DESC").Find(&reviews)

	var avg float64
	if len(reviews) > 0 {
		var sum int
		for _, r := range reviews {
			sum += r.Rating
		}
		avg = float64(sum) / float64(len(reviews))
	}

	c.JSON(http.StatusOK, gin.H{
		"reviews":       reviews,
		"average_rating": avg,
		"total":         len(reviews),
	})
}
