package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/models"
)

// getActorUser resolves firebase_uid → User row (shared helper).
func getActorUser(c *gin.Context) (*models.User, bool) {
	firebaseUID := c.GetString("firebase_uid")
	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return nil, false
	}
	return &user, true
}

// FollowUser - Seguir um utilizador
//
// @Summary      Seguir utilizador
// @Description  O utilizador autenticado passa a seguir o utilizador com o ID indicado
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  int  true  "ID do utilizador a seguir"
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /users/{id}/follow [post]
func FollowUser(c *gin.Context) {
	me, ok := getActorUser(c)
	if !ok {
		return
	}

	targetID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || uint(targetID) == me.ID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID inválido."})
		return
	}

	// Confirm target exists
	var target models.User
	if err := database.DB.First(&target, targetID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	// Idempotent: do nothing if already following
	var existing models.Follow
	if database.DB.Where("follower_id = ? AND following_id = ?", me.ID, targetID).First(&existing).Error == nil {
		c.JSON(http.StatusOK, gin.H{"message": "Já está a seguir este utilizador."})
		return
	}

	follow := models.Follow{FollowerID: me.ID, FollowingID: uint(targetID)}
	if err := database.DB.Create(&follow).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao seguir utilizador."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "A seguir utilizador com sucesso."})
}

// UnfollowUser - Deixar de seguir um utilizador
//
// @Summary      Deixar de seguir utilizador
// @Description  O utilizador autenticado deixa de seguir o utilizador com o ID indicado
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  int  true  "ID do utilizador"
// @Success      200  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /users/{id}/follow [delete]
func UnfollowUser(c *gin.Context) {
	me, ok := getActorUser(c)
	if !ok {
		return
	}

	targetID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID inválido."})
		return
	}

	database.DB.Where("follower_id = ? AND following_id = ?", me.ID, targetID).Delete(&models.Follow{})
	c.JSON(http.StatusOK, gin.H{"message": "Deixou de seguir o utilizador."})
}

// GetFollowStatus - Verifica se o utilizador autenticado segue o alvo
//
// @Summary      Estado de seguidor
// @Description  Devolve se o utilizador autenticado está a seguir o utilizador indicado
// @Tags         users
// @Produce      json
// @Security     BearerAuth
// @Param        id   path  int  true  "ID do utilizador alvo"
// @Success      200  {object}  map[string]interface{}
// @Router       /users/{id}/follow/status [get]
func GetFollowStatus(c *gin.Context) {
	me, ok := getActorUser(c)
	if !ok {
		return
	}

	targetID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var existing models.Follow
	isFollowing := database.DB.Where("follower_id = ? AND following_id = ?", me.ID, targetID).First(&existing).Error == nil

	c.JSON(http.StatusOK, gin.H{"is_following": isFollowing})
}

// GetFollowers - Lista os seguidores de um utilizador
//
// @Summary      Listar seguidores
// @Description  Devolve a lista de utilizadores que seguem o utilizador indicado
// @Tags         users
// @Produce      json
// @Param        id   path  int  true  "ID do utilizador"
// @Success      200  {object}  map[string]interface{}
// @Router       /users/{id}/followers [get]
func GetFollowers(c *gin.Context) {
	userID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID inválido."})
		return
	}

	var follows []models.Follow
	database.DB.Preload("Follower").Where("following_id = ?", userID).Find(&follows)

	users := make([]models.User, 0, len(follows))
	for _, f := range follows {
		u := f.Follower
		u.Email = "" // omit private field
		users = append(users, u)
	}

	c.JSON(http.StatusOK, gin.H{"count": len(users), "users": users})
}

// GetFollowing - Lista os utilizadores que o utilizador indicado segue
//
// @Summary      Listar seguidos
// @Description  Devolve a lista de utilizadores que o utilizador indicado está a seguir
// @Tags         users
// @Produce      json
// @Param        id   path  int  true  "ID do utilizador"
// @Success      200  {object}  map[string]interface{}
// @Router       /users/{id}/following [get]
func GetFollowing(c *gin.Context) {
	userID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID inválido."})
		return
	}

	var follows []models.Follow
	database.DB.Preload("Following").Where("follower_id = ?", userID).Find(&follows)

	users := make([]models.User, 0, len(follows))
	for _, f := range follows {
		u := f.Following
		u.Email = ""
		users = append(users, u)
	}

	c.JSON(http.StatusOK, gin.H{"count": len(users), "users": users})
}

// GetFollowCounts - Devolve os contadores de seguidores/seguidos de um utilizador
//
// @Summary      Contadores de follow
// @Description  Devolve o número de seguidores e de utilizadores seguidos
// @Tags         users
// @Produce      json
// @Param        id   path  int  true  "ID do utilizador"
// @Success      200  {object}  map[string]interface{}
// @Router       /users/{id}/follow/counts [get]
func GetFollowCounts(c *gin.Context) {
	userID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ID inválido."})
		return
	}

	var followers, following int64
	database.DB.Model(&models.Follow{}).Where("following_id = ?", userID).Count(&followers)
	database.DB.Model(&models.Follow{}).Where("follower_id = ?", userID).Count(&following)

	c.JSON(http.StatusOK, gin.H{"followers": followers, "following": following})
}
