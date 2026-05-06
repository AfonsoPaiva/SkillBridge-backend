package handlers

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/email"
	"github.com/paiva/SkillBridge/Backend/internal/models"
	"github.com/paiva/SkillBridge/Backend/internal/storage"
	"gorm.io/gorm"
)

type projectRoleInput struct {
	Title       string   `json:"title"`
	SkillNames  []string `json:"skill_names"`
	SkillName   string   `json:"skill_name"`
	Description string   `json:"description"`
	Spots       int      `json:"spots"`
}

func normalizeProjectRoleSkillNames(skillNames []string, legacySkillName string) models.StringList {
	if len(skillNames) == 0 {
		legacySkillName = strings.TrimSpace(legacySkillName)
		if legacySkillName != "" {
			skillNames = []string{legacySkillName}
		}
	}

	normalized := make(models.StringList, 0, len(skillNames))
	seen := make(map[string]struct{}, len(skillNames))
	for _, skill := range skillNames {
		skill = strings.TrimSpace(skill)
		if skill == "" {
			continue
		}
		key := strings.ToLower(skill)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, skill)
	}

	return normalized
}

func prepareProjectRoles(inputs []projectRoleInput, requireSkills bool) ([]models.ProjectRole, error) {
	roles := make([]models.ProjectRole, 0, len(inputs))
	for _, input := range inputs {
		skillNames := normalizeProjectRoleSkillNames(input.SkillNames, input.SkillName)
		title := strings.TrimSpace(input.Title)

		if title == "" && len(skillNames) == 0 {
			if requireSkills {
				return nil, fmt.Errorf("cada vaga tem de incluir pelo menos uma competência")
			}
			continue
		}
		if requireSkills && len(skillNames) == 0 {
			return nil, fmt.Errorf("cada vaga tem de incluir pelo menos uma competência")
		}
		for _, skill := range skillNames {
			if !config.IsValidSkill(skill) {
				return nil, fmt.Errorf("competência não reconhecida: %s", skill)
			}
		}

		spots := input.Spots
		if spots < 1 {
			spots = 1
		}

		roles = append(roles, models.ProjectRole{
			Title:       title,
			SkillNames:  skillNames,
			Description: input.Description,
			Spots:       spots,
		})
	}

	return roles, nil
}

// ensureUniqueSlug checks if a slug is unique and appends a counter if needed.
// excludeID allows updating a project without conflicting with itself.
func ensureUniqueSlug(baseSlug string, excludeID uint) string {
	slug := baseSlug
	counter := 1

	for {
		var count int64
		query := database.DB.Model(&models.Project{}).Where("slug = ?", slug)
		if excludeID != 0 {
			query = query.Where("id != ?", excludeID)
		}
		query.Count(&count)

		if count == 0 {
			return slug
		}

		slug = fmt.Sprintf("%s-%d", baseSlug, counter)
		counter++
	}
}

// ensureUniqueUserSlug checks if a user slug is unique and appends a counter if needed.
// excludeID allows updating a user without conflicting with itself.
func ensureUniqueUserSlug(baseSlug string, excludeID uint) string {
	slug := baseSlug
	counter := 1

	for {
		var count int64
		query := database.DB.Model(&models.User{}).Where("slug = ?", slug)
		if excludeID != 0 {
			query = query.Where("id != ?", excludeID)
		}
		query.Count(&count)

		if count == 0 {
			return slug
		}

		slug = fmt.Sprintf("%s-%d", baseSlug, counter)
		counter++
	}
}

// CreateProject - Cria um novo projeto
//
// @Summary      Criar projeto
// @Description  Cria um novo projeto
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        input  body  object{title=string,description=string}  true  "Dados do projeto"
// @Success      201  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /projects [post]
func CreateProject(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var owner models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&owner).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var input struct {
		Title       string             `json:"title" binding:"required"`
		Description string             `json:"description"`
		ImageURL    string             `json:"image_url"`
		Status      string             `json:"status"`
		Roles       []projectRoleInput `json:"roles"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	roles, err := prepareProjectRoles(input.Roles, false)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	status := input.Status
	if status == "" {
		status = "open"
	}

	// Generate unique slug
	slug := models.GenerateSlug(input.Title)
	slug = ensureUniqueSlug(slug, 0)

	project := models.Project{
		OwnerID:     owner.ID,
		Title:       input.Title,
		Slug:        slug,
		Description: input.Description,
		ImageURL:    input.ImageURL,
		Status:      status,
	}

	database.DB.Create(&project)

	// Save inline roles
	for i := range roles {
		roles[i].ProjectID = project.ID
		database.DB.Create(&roles[i])
	}

	// Registar o criador como primeiro proprietário
	projectOwner := models.ProjectOwner{
		ProjectID: project.ID,
		UserID:    owner.ID,
	}
	database.DB.Create(&projectOwner)

	// Return project with roles preloaded
	database.DB.Preload("Roles").First(&project, project.ID)
	c.JSON(http.StatusCreated, gin.H{"message": "Projeto criado.", "project": project})
}

// GetProjects - Lista projetos com filtros opcionais
//
// @Summary      Listar projetos
// @Description  Lista projetos — filtra por estado e/ou skill_id
// @Tags         projects
// @Produce      json
// @Param        status    query  string  false  "Estado: open (default) / in_progress / completed / all"
// @Param        skill_id  query  int     false  "Filtrar projetos que procuram esta skill"
// @Success      200  {array}   models.Project
// @Router       /projects [get]
func GetProjects(c *gin.Context) {
	status := c.DefaultQuery("status", "open")
	skillID := c.Query("skill_id")

	query := database.DB.Preload("Owner").Preload("Roles")
	if status != "all" {
		query = query.Where("status = ?", status)
	}
	if skillID != "" {
		query = query.Where("id IN (SELECT project_id FROM project_roles WHERE skill_id = ? AND filled = false)", skillID)
	}

	var projects []models.Project
	query.Order("created_at DESC").Find(&projects)
	c.JSON(http.StatusOK, projects)
}

// GetProjectByID - Detalhes de um projeto
//
// @Summary      Obter projeto por ID ou slug
// @Description  Devolve os detalhes de um projeto (busca por slug ou ID)
// @Tags         projects
// @Produce      json
// @Param        id   path      string  true  "Slug ou ID do projeto"
// @Success      200  {object}  models.Project
// @Failure      404  {object}  map[string]string
// @Router       /projects/{id} [get]
func GetProjectByID(c *gin.Context) {
	var project models.Project
	param := c.Param("id")

	// Try to find by slug first, fallback to ID
	err := database.DB.Preload("Owner").Preload("Roles").Preload("Members.User").Where("slug = ?", param).First(&project).Error
	if err != nil {
		// Try by ID as fallback
		err = database.DB.Preload("Owner").Preload("Roles").Preload("Members.User").First(&project, param).Error
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Projeto não encontrado."})
			return
		}
	}
	c.JSON(http.StatusOK, project)
}

// GetProjectMembers - Lista os membros aceites de um projeto (público)
//
// @Summary      Listar membros do projeto
// @Description  Devolve os membros com candidatura aceite de um projeto
// @Tags         projects
// @Produce      json
// @Param        id  path  int  true  "ID do projeto"
// @Success      200  {array}   models.ProjectMember
// @Failure      404  {object}  map[string]string
// @Router       /projects/{id}/members [get]
func GetProjectMembers(c *gin.Context) {
	var project models.Project
	param := c.Param("id")

	// Try to find by slug first, fallback to ID
	err := database.DB.Where("slug = ?", param).First(&project).Error
	if err != nil {
		// Try by ID as fallback
		err = database.DB.First(&project, param).Error
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Projeto não encontrado."})
			return
		}
	}
	var members []models.ProjectMember
	database.DB.Preload("User").Where("project_id = ? AND status = ?", project.ID, "accepted").Find(&members)
	// Omit emails from public response
	for i := range members {
		members[i].User.Email = ""
	}
	c.JSON(http.StatusOK, members)
}

// JoinProject - Utilizador candidata-se a um projeto
//
// @Summary      Candidatar a projeto
// @Description  Envia uma candidatura (estado pending) para participar num projeto
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id     path  int                     true  "ID do projeto"
// @Param        input  body  object{role_id=integer}  false "Role pretendida"
// @Success      201  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      409  {object}  map[string]string
// @Router       /projects/{id}/join [post]
func JoinProject(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var project models.Project
	param := c.Param("id")

	// Try to find by slug first, fallback to ID
	err := database.DB.Preload("Owners").Where("slug = ?", param).First(&project).Error
	if err != nil {
		// Try by ID as fallback
		err = database.DB.Preload("Owners").First(&project, param).Error
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Projeto não encontrado."})
			return
		}
	}

	// Block owners (main + co-owners) from applying to their own project
	if project.OwnerID == user.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Não podes candidatar-te a um projeto que criaste."})
		return
	}
	for _, o := range project.Owners {
		if o.UserID == user.ID {
			c.JSON(http.StatusForbidden, gin.H{"error": "Não podes candidatar-te a um projeto em que és co-proprietário."})
			return
		}
	}

	var input struct {
		RoleID uint `json:"role_id"`
	}
	c.ShouldBindJSON(&input)

	// Verificar candidatura duplicada por vaga (permite candidatar a múltiplas vagas do mesmo projeto)
	var existing models.ProjectMember
	dupQuery := database.DB.Where("project_id = ? AND user_id = ?", project.ID, user.ID)
	if input.RoleID != 0 {
		dupQuery = dupQuery.Where("role_id = ?", input.RoleID)
	}
	if dupQuery.First(&existing).Error == nil {
		msg := "Já tens uma candidatura neste projeto."
		if input.RoleID != 0 {
			msg = "Já tens uma candidatura para esta vaga."
		}
		c.JSON(http.StatusConflict, gin.H{"error": msg, "status": existing.Status})
		return
	}

	member := models.ProjectMember{
		ProjectID: project.ID,
		UserID:    user.ID,
		RoleID:    input.RoleID,
		Status:    "pending",
	}
	database.DB.Create(&member)

	// Create (or reuse) conversation between applicant and project owner,
	// then post a system message so the owner can accept/reject in-chat.
	var projectOwner models.User
	if database.DB.First(&projectOwner, project.OwnerID).Error == nil && projectOwner.ID != user.ID {
		userAID, userBID := user.ID, projectOwner.ID
		if userAID > userBID {
			userAID, userBID = userBID, userAID
		}
		var conv models.Conversation
		if database.DB.Where("user_a_id = ? AND user_b_id = ?", userAID, userBID).First(&conv).Error != nil {
			conv = models.Conversation{UserAID: userAID, UserBID: userBID}
			database.DB.Create(&conv)
		}

		// Build the notification text
		roleName := ""
		if input.RoleID != 0 {
			var role models.ProjectRole
			if database.DB.First(&role, input.RoleID).Error == nil {
				roleName = role.Title
			}
		}
		text := fmt.Sprintf("Olá! Quero juntar-me ao projeto \"%s\"", project.Title)
		if roleName != "" {
			text += fmt.Sprintf(" para a função \"%s\"", roleName)
		}
		text += ". Podes aceitar ou rejeitar a minha candidatura diretamente aqui."
		encoded := base64.StdEncoding.EncodeToString([]byte(text))
		memberID := member.ID
		database.DB.Create(&models.Message{
			ConversationID:   conv.ID,
			SenderID:         user.ID,
			EncryptedContent: encoded,
			EphemeralKey:     "plain",
			IsSystem:         true,
			MessageType:      "application",
			MetaProjectID:    &project.ID,
			MetaMemberID:     &memberID,
			MetaStatus:       "pending",
		})

		// Send email notification to project owner (best-effort, non-blocking)
		go func() {
			projectURL := fmt.Sprintf("%s/projects/%s", config.AppConfig.FrontendURL, project.Slug)
			if err := email.SendProjectApplication(projectOwner.Email, projectOwner.Name, project.Title, user.Name, projectURL); err != nil {
				log.Printf("[email] Erro ao enviar email de candidatura para %s: %v", projectOwner.Email, err)
			}
		}()
	}

	c.JSON(http.StatusCreated, gin.H{"message": "Candidatura enviada. Aguarda aprovação do proprietário."})
}

// AddProjectOwner - Adiciona um co-proprietário ao projeto
//
// @Summary      Adicionar co-proprietário
// @Description  Um proprietário existente pode adicionar outro utilizador como co-proprietário
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id     path  int                      true  "ID do projeto"
// @Param        input  body  object{user_id=integer}  true  "ID do utilizador a adicionar"
// @Success      201  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Failure      409  {object}  map[string]string
// @Router       /projects/{id}/owners [post]
func AddProjectOwner(c *gin.Context) {
	project, ok := ownerGuard(c)
	if !ok {
		return
	}

	var input struct {
		UserID uint `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var newOwner models.User
	if err := database.DB.First(&newOwner, input.UserID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var existing models.ProjectOwner
	if database.DB.Where("project_id = ? AND user_id = ?", project.ID, input.UserID).First(&existing).Error == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Utilizador já é proprietário deste projeto."})
		return
	}

	database.DB.Create(&models.ProjectOwner{ProjectID: project.ID, UserID: input.UserID})
	c.JSON(http.StatusCreated, gin.H{"message": "Co-proprietário adicionado com sucesso."})
}

// UpdateProject - Atualiza um projeto (owner)
//
// @Summary      Atualizar projeto
// @Description  Atualiza título ou descrição — apenas proprietários
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id     path  int                                         true  "ID do projeto"
// @Param        input  body  object{title=string,description=string}  false "Campos a atualizar"
// @Success      200  {object}  map[string]interface{}
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /projects/{id} [put]
func UpdateProject(c *gin.Context) {
	project, ok := ownerGuard(c)
	if !ok {
		return
	}
	var input struct {
		Title       string             `json:"title"`
		Description string             `json:"description"`
		ImageURL    *string            `json:"image_url"` // Pointer to distinguish between not provided and empty
		Status      string             `json:"status"`
		Roles       []projectRoleInput `json:"roles"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var roles []models.ProjectRole
	if input.Roles != nil {
		preparedRoles, err := prepareProjectRoles(input.Roles, false)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		roles = preparedRoles
	}
	if input.Title != "" {
		project.Title = input.Title
		// Regenerate slug when title changes
		newSlug := models.GenerateSlug(input.Title)
		project.Slug = ensureUniqueSlug(newSlug, project.ID)
	}
	if input.Description != "" {
		project.Description = input.Description
	}

	// Handle image URL update or removal
	// Only process if image_url field was explicitly provided in the request
	if input.ImageURL != nil {
		newImageURL := *input.ImageURL

		// Delete old image when the URL changes (including removal with empty string)
		if project.ImageURL != "" && newImageURL != project.ImageURL {
			oldObjectName := extractGCSObjectName(project.ImageURL)
			if oldObjectName != "" {
				if err := storage.DeleteFile(oldObjectName); err != nil {
					log.Printf("Warning: Failed to delete old project image %s: %v", oldObjectName, err)
				}
			}
		}
		// Update the image URL (can be empty to remove the image from the project)
		project.ImageURL = newImageURL
	}

	if input.Status != "" {
		project.Status = input.Status
	}
	database.DB.Save(&project)

	// Sync roles: collect existing role IDs, remove their members, then delete all and re-insert
	if input.Roles != nil {
		var existingRoles []models.ProjectRole
		database.DB.Where("project_id = ?", project.ID).Find(&existingRoles)
		if len(existingRoles) > 0 {
			existingRoleIDs := make([]uint, len(existingRoles))
			for i, r := range existingRoles {
				existingRoleIDs[i] = r.ID
			}
			// Remove all member applications assigned to roles that are being deleted
			database.DB.Where("role_id IN ?", existingRoleIDs).Delete(&models.ProjectMember{})
		}
		database.DB.Where("project_id = ?", project.ID).Delete(&models.ProjectRole{})
		for i := range roles {
			roles[i].ProjectID = project.ID
			database.DB.Create(&roles[i])
		}
	}

	database.DB.Preload("Roles").First(&project, project.ID)
	c.JSON(http.StatusOK, gin.H{"message": "Projeto atualizado.", "project": project})
}

// DeleteProject - Elimina um projeto (owner)
//
// @Summary      Eliminar projeto
// @Description  Remove permanentemente um projeto — apenas proprietários
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int  true  "ID do projeto"
// @Success      200  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /projects/{id} [delete]
func DeleteProject(c *gin.Context) {
	project, ok := ownerGuard(c)
	if !ok {
		return
	}
	// Delete project image from GCS
	if project.ImageURL != "" {
		objectName := extractGCSObjectName(project.ImageURL)
		if objectName != "" {
			if err := storage.DeleteFile(objectName); err != nil {
				log.Printf("Warning: Failed to delete project image %s: %v", objectName, err)
			}
		}
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

// UpdateProjectStatus - Altera o estado de um projeto (owner)
//
// @Summary      Alterar estado do projeto
// @Description  Muda o estado: open / in_progress / completed — apenas proprietários
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id     path  int                       true  "ID do projeto"
// @Param        input  body  object{status=string}  true  "Novo estado"
// @Success      200  {object}  map[string]interface{}
// @Failure      400  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Router       /projects/{id}/status [put]
func UpdateProjectStatus(c *gin.Context) {
	project, ok := ownerGuard(c)
	if !ok {
		return
	}
	var input struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	allowed := map[string]bool{"open": true, "in_progress": true, "completed": true}
	if !allowed[input.Status] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Estado inválido. Use: open, in_progress ou completed."})
		return
	}
	database.DB.Model(&project).Update("status", input.Status)
	c.JSON(http.StatusOK, gin.H{"message": "Estado atualizado.", "status": input.Status})
}

// CreateProjectRole - Adiciona uma role ao projeto (owner)
//
// @Summary      Criar role no projeto
// @Description  Define um perfil que o projeto procura — apenas proprietários
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id     path  int                                               true  "ID do projeto"
// @Param        input  body  object{skill_names=[]string,description=string}  true  "Dados da role"
// @Success      201  {object}  models.ProjectRole
// @Failure      400  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Router       /projects/{id}/roles [post]
func CreateProjectRole(c *gin.Context) {
	project, ok := ownerGuard(c)
	if !ok {
		return
	}
	var input projectRoleInput
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	roles, err := prepareProjectRoles([]projectRoleInput{input}, true)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	role := roles[0]
	role.ProjectID = project.ID
	database.DB.Create(&role)
	c.JSON(http.StatusCreated, role)
}

// DeleteProjectRole - Remove uma role do projeto (owner)
//
// @Summary      Remover role do projeto
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        id       path  int  true  "ID do projeto"
// @Param        role_id  path  int  true  "ID da role"
// @Success      200  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /projects/{id}/roles/{role_id} [delete]
func DeleteProjectRole(c *gin.Context) {
	project, ok := ownerGuard(c)
	if !ok {
		return
	}
	var role models.ProjectRole
	if err := database.DB.Where("id = ? AND project_id = ?", c.Param("role_id"), project.ID).First(&role).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Role não encontrada."})
		return
	}
	// Remove all member applications (any status) assigned to this role
	database.DB.Where("role_id = ?", role.ID).Delete(&models.ProjectMember{})
	database.DB.Delete(&role)
	c.JSON(http.StatusOK, gin.H{"message": "Role removida."})
}

// GetApplications - Lista candidaturas de um projeto (owner)
//
// @Summary      Listar candidaturas
// @Description  Devolve as candidaturas do projeto filtráveis por estado — apenas proprietários
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        id      path   int     true   "ID do projeto"
// @Param        status  query  string  false  "pending (default) / accepted / rejected / all"
// @Success      200  {array}   models.ProjectMember
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /projects/{id}/applications [get]
func GetApplications(c *gin.Context) {
	project, ok := ownerGuard(c)
	if !ok {
		return
	}
	status := c.DefaultQuery("status", "pending")
	query := database.DB.Preload("User").Where("project_id = ?", project.ID)
	if status != "all" {
		query = query.Where("status = ?", status)
	}
	var members []models.ProjectMember
	query.Find(&members)
	for i := range members {
		members[i].User.Email = ""
	}
	c.JSON(http.StatusOK, members)
}

// RespondApplication - Aceita ou rejeita uma candidatura (owner)
//
// @Summary      Responder a candidatura
// @Description  Aceita ou rejeita a candidatura de um utilizador — apenas proprietários
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id         path  int                       true  "ID do projeto"
// @Param        member_id  path  int                       true  "ID da candidatura"
// @Param        input      body  object{action=string}  true  "accept ou reject"
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /projects/{id}/applications/{member_id} [put]
func RespondApplication(c *gin.Context) {
	project, ok := ownerGuard(c)
	if !ok {
		return
	}
	var input struct {
		Action string `json:"action" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if input.Action != "accept" && input.Action != "reject" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Ação inválida. Use 'accept' ou 'reject'."})
		return
	}
	var member models.ProjectMember
	if err := database.DB.Where("id = ? AND project_id = ?", c.Param("member_id"), project.ID).First(&member).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Candidatura não encontrada."})
		return
	}

	previousStatus := member.Status
	newStatus := "accepted"
	if input.Action == "reject" {
		newStatus = "rejected"
	}

	// Só enviar email se houve transição real (tipicamente pending -> accepted/rejected)
	shouldNotifyDecision := previousStatus == "pending" && (newStatus == "accepted" || newStatus == "rejected")

	if previousStatus == "accepted" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Esta candidatura já foi aceite e não pode ser alterada."})
		return
	}

	database.DB.Model(&member).Update("status", newStatus)

	// Atualizar o status na mensagem de candidatura
	database.DB.Model(&models.Message{}).
		Where("meta_member_id = ? AND message_type = ?", member.ID, "application").
		Update("meta_status", newStatus)

	if newStatus == "accepted" && member.RoleID != 0 {
		database.DB.Model(&models.ProjectRole{}).Where("id = ? AND filled < spots", member.RoleID).UpdateColumn("filled", gorm.Expr("filled + 1"))
	} else if newStatus == "rejected" && member.RoleID != 0 {
		// Rejeitar só deve acontecer a pending; portanto não decrementamos aqui.
	}

	// Email ao candidato quando o owner decide aceitar/rejeitar
	if shouldNotifyDecision {
		go func(candidateID uint, decision string, roleID uint, projectID uint, ownerID uint) {
			var candidate models.User
			if err := database.DB.First(&candidate, candidateID).Error; err != nil {
				return
			}

			var owner models.User
			if err := database.DB.First(&owner, ownerID).Error; err != nil {
				owner.Name = ""
			}

			projectURL := fmt.Sprintf("%s/projects/%s", config.AppConfig.FrontendURL, project.Slug)

			if decision == "accepted" {
				_ = email.SendProjectDecisionApproved(candidate.Email, candidate.Name, project.Title, projectURL, owner.Name)
			} else if decision == "rejected" {
				_ = email.SendProjectDecisionRejected(candidate.Email, candidate.Name, project.Title, projectURL, owner.Name)
			}
		}(member.UserID, newStatus, member.RoleID, project.ID, project.OwnerID)
	}

	// Verificar se o projeto ficou cheio
	checkAndUpdateProjectFullStatus(project.ID)

	c.JSON(http.StatusOK, gin.H{"message": "Candidatura " + newStatus + ".", "status": newStatus})
}

// ownerGuard carrega o projeto e verifica que o utilizador autenticado é proprietário.
func ownerGuard(c *gin.Context) (models.Project, bool) {
	firebaseUID := c.GetString("firebase_uid")
	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return models.Project{}, false
	}
	var project models.Project
	param := c.Param("id")

	// Try to find by slug first, fallback to ID
	err := database.DB.Preload("Owners").Where("slug = ?", param).First(&project).Error
	if err != nil {
		// Try by ID as fallback
		err = database.DB.Preload("Owners").First(&project, param).Error
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Projeto não encontrado."})
			return models.Project{}, false
		}
	}

	for _, o := range project.Owners {
		if o.UserID == user.ID {
			return project, true
		}
	}
	c.JSON(http.StatusForbidden, gin.H{"error": "Não tens permissão para gerir este projeto."})
	return models.Project{}, false
}

// checkAndUpdateProjectFullStatus verifica se todas as vagas estão preenchidas e atualiza o status
func checkAndUpdateProjectFullStatus(projectID uint) {
	var roles []models.ProjectRole
	database.DB.Where("project_id = ?", projectID).Find(&roles)

	if len(roles) == 0 {
		return
	}

	allFull := true
	for _, role := range roles {
		if role.Filled < role.Spots {
			allFull = false
			break
		}
	}

	var project models.Project
	database.DB.First(&project, projectID)

	// Atualizar status para "full" se todas as vagas estão preenchidas e status é "open"
	if allFull && project.Status == "open" {
		database.DB.Model(&project).Update("status", "full")
	} else if !allFull && project.Status == "full" {
		// Voltar a "open" se havia vagas cheias mas agora há vagas livres
		database.DB.Model(&project).Update("status", "open")
	}
}

// RemoveProjectMember - Remove um membro do projeto (owner only)
//
// @Summary      Remover membro
// @Description  Remove um membro aceite do projeto — apenas proprietários
// @Tags         projects
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id         path  int  true  "ID do projeto"
// @Param        member_id  path  int  true  "ID do membro"
// @Success      200  {object}  map[string]string
// @Failure      400  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /projects/{id}/members/{member_id} [delete]
func RemoveProjectMember(c *gin.Context) {
	project, ok := ownerGuard(c)
	if !ok {
		return
	}

	var member models.ProjectMember
	if err := database.DB.Where("id = ? AND project_id = ?", c.Param("member_id"), project.ID).First(&member).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Membro não encontrado."})
		return
	}

	// Não permitir remover se não está aceite
	if member.Status != "accepted" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Apenas membros aceites podem ser removidos."})
		return
	}

	// Decrementar filled se o membro estava numa role
	if member.RoleID != 0 {
		database.DB.Model(&models.ProjectRole{}).Where("id = ? AND filled > 0", member.RoleID).UpdateColumn("filled", gorm.Expr("filled - 1"))
	}

	// Remover o membro
	database.DB.Delete(&member)

	// Verificar se o projeto ainda está cheio
	checkAndUpdateProjectFullStatus(project.ID)

	c.JSON(http.StatusOK, gin.H{"message": "Membro removido com sucesso."})
}

// GetMyApplications - Devolve as candidaturas do utilizador autenticado num projeto específico
//
// @Summary      As minhas candidaturas
// @Description  Devolve todas as candidaturas do utilizador autenticado para um projeto
// @Tags         projects
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  string  true  "Slug ou ID do projeto"
// @Success      200  {array}   models.ProjectMember
// @Failure      404  {object}  map[string]string
// @Router       /projects/{id}/my-applications [get]
func GetMyApplications(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")
	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var project models.Project
	param := c.Param("id")
	err := database.DB.Where("slug = ?", param).First(&project).Error
	if err != nil {
		err = database.DB.First(&project, param).Error
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Projeto não encontrado."})
			return
		}
	}

	var applications []models.ProjectMember
	database.DB.Where("project_id = ? AND user_id = ?", project.ID, user.ID).Find(&applications)
	c.JSON(http.StatusOK, applications)
}
