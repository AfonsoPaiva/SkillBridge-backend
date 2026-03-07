package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/models"
)

// ListSkills returns the full predefined skill list.
//
// @Summary      Listar competências disponíveis
// @Description  Devolve a lista predefinida de competências que os utilizadores podem selecionar
// @Tags         skills
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Router       /skills [get]
func ListSkills(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"count":    len(config.Skills),
		"sections": config.SkillSections,
		"skills":   config.Skills,
	})
}

// AddUserSkill adds a skill string to the authenticated user's profile.
//
// @Summary      Adicionar competência ao perfil
// @Tags         skills
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        input  body  object{skill=string}  true  "Nome da competência"
// @Success      200  {object}  models.User
// @Failure      400  {object}  map[string]string
// @Router       /users/me/skills [post]
func AddUserSkill(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var input struct {
		Skill string `json:"skill" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if !config.IsValidSkill(input.Skill) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Competência não reconhecida."})
		return
	}

	// Check duplicate
	for _, s := range user.Skills {
		if s == input.Skill {
			c.JSON(http.StatusConflict, gin.H{"error": "Competência já adicionada."})
			return
		}
	}

	user.Skills = append(user.Skills, input.Skill)
	database.DB.Model(&user).Update("skills", user.Skills)
	c.JSON(http.StatusOK, gin.H{"skills": user.Skills})
}

// RemoveUserSkill removes a skill string from the authenticated user's profile.
//
// @Summary      Remover competência do perfil
// @Tags         skills
// @Produce      json
// @Security     BearerAuth
// @Param        skill  query  string  true  "Nome da competência a remover"
// @Success      200  {object}  map[string]interface{}
// @Failure      404  {object}  map[string]string
// @Router       /users/me/skills [delete]
func RemoveUserSkill(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	skillName := c.Query("skill")
	if skillName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Parâmetro 'skill' obrigatório."})
		return
	}

	updated := models.StringList{}
	found := false
	for _, s := range user.Skills {
		if s == skillName {
			found = true
			continue
		}
		updated = append(updated, s)
	}
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "Competência não encontrada no perfil."})
		return
	}

	database.DB.Model(&user).Update("skills", updated)
	c.JSON(http.StatusOK, gin.H{"skills": updated})
}
