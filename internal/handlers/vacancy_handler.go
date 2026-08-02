package handlers

import (
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/models"
)

// ListMyVacancies returns all vacancies belonging to the authenticated recruiter.
// GET /api/recruiter/vacancies
func ListMyVacancies(c *gin.Context) {
	recruiterID := c.GetString("recruiter_id")

	var vacancies []models.Vacancy
	if err := database.DB.Where("recruiter_id = ?", recruiterID).
		Order("published_at DESC").
		Find(&vacancies).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao listar vagas."})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"vacancies": vacancies,
		"count":     len(vacancies),
	})
}

// CreateVacancy creates a new vacancy for the authenticated recruiter.
// POST /api/recruiter/vacancies
func CreateVacancy(c *gin.Context) {
	recruiterID := c.GetString("recruiter_id")

	var input struct {
		Title          string   `json:"title" binding:"required"`
		Type           string   `json:"type" binding:"required"`
		Tags           []string `json:"tags"`
		Description    string   `json:"description" binding:"required"`
		ApplicationURL string   `json:"application_url" binding:"required"`
		Region         string   `json:"region"`
		WorkMode       string   `json:"work_mode"`
		EmploymentType string   `json:"employment_type"`
		Deadline       *string  `json:"deadline"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Campos obrigatórios em falta.", "details": err.Error()})
		return
	}

	// Validate type
	validTypes := map[string]bool{
		"summer_internship":          true,
		"curricular_internship":      true,
		"extracurricular_internship": true,
		"junior_position":            true,
	}
	if !validTypes[input.Type] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Tipo de vaga inválido."})
		return
	}



	// Parse optional deadline
	var deadline *time.Time
	if input.Deadline != nil && *input.Deadline != "" {
		t, err := time.Parse("2006-01-02", *input.Deadline)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Formato de data inválido (usar AAAA-MM-DD)."})
			return
		}
		deadline = &t
	}

	now := time.Now()
	vacancy := models.Vacancy{
		RecruiterID:    recruiterID,
		Title:          input.Title,
		Type:           input.Type,
		Tags:           models.StringList(input.Tags),
		Description:    input.Description,
		ApplicationURL: input.ApplicationURL,
		Region:         input.Region,
		WorkMode:       input.WorkMode,
		EmploymentType: input.EmploymentType,
		Deadline:       deadline,
		Status:         "active",
		PublishedAt:    now,
		ExpiresAt:      now.Add(30 * 24 * time.Hour), // 30 days
	}

	if err := database.DB.Create(&vacancy).Error; err != nil {
		log.Printf("[vacancy] Erro ao criar vaga: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao criar vaga."})
		return
	}

	log.Printf("[vacancy] Nova vaga criada: %s (recruiter: %s)", vacancy.Title, recruiterID)
	c.JSON(http.StatusCreated, gin.H{"vacancy": vacancy})
}

// UpdateVacancy updates an existing vacancy belonging to the authenticated recruiter.
// PUT /api/recruiter/vacancies/:id
func UpdateVacancy(c *gin.Context) {
	recruiterID := c.GetString("recruiter_id")
	vacancyID := c.Param("id")

	var vacancy models.Vacancy
	if err := database.DB.Where("id = ? AND recruiter_id = ?", vacancyID, recruiterID).First(&vacancy).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Vaga não encontrada."})
		return
	}

	var input struct {
		Title          *string  `json:"title"`
		Type           *string  `json:"type"`
		Tags           []string `json:"tags"`
		Description    *string  `json:"description"`
		ApplicationURL *string  `json:"application_url"`
		Region         *string  `json:"region"`
		WorkMode       *string  `json:"work_mode"`
		EmploymentType *string  `json:"employment_type"`
		Deadline       *string  `json:"deadline"`
		Status         *string  `json:"status"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados inválidos."})
		return
	}

	updates := make(map[string]interface{})

	if input.Title != nil {
		updates["title"] = *input.Title
	}
	if input.Type != nil {
		validTypes := map[string]bool{
			"summer_internship":          true,
			"curricular_internship":      true,
			"extracurricular_internship": true,
			"junior_position":            true,
		}
		if !validTypes[*input.Type] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Tipo de vaga inválido."})
			return
		}
		updates["type"] = *input.Type
	}
	if input.Tags != nil {
		updates["tags"] = models.StringList(input.Tags)
	}
	if input.Description != nil {

		updates["description"] = *input.Description
	}
	if input.ApplicationURL != nil {
		updates["application_url"] = *input.ApplicationURL
	}
	if input.Region != nil {
		updates["region"] = *input.Region
	}
	if input.WorkMode != nil {
		updates["work_mode"] = *input.WorkMode
	}
	if input.EmploymentType != nil {
		updates["employment_type"] = *input.EmploymentType
	}
	if input.Deadline != nil {
		if *input.Deadline == "" {
			updates["deadline"] = nil
		} else {
			t, err := time.Parse("2006-01-02", *input.Deadline)
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Formato de data inválido."})
				return
			}
			updates["deadline"] = t
		}
	}
	if input.Status != nil {
		validStatuses := map[string]bool{"active": true, "archived": true}
		if !validStatuses[*input.Status] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Estado inválido."})
			return
		}
		updates["status"] = *input.Status
		// If reactivating, reset expiry
		if *input.Status == "active" {
			updates["expires_at"] = time.Now().Add(30 * 24 * time.Hour)
		}
	}

	if err := database.DB.Model(&vacancy).Updates(updates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao atualizar vaga."})
		return
	}

	// Reload
	database.DB.Where("id = ?", vacancyID).First(&vacancy)
	c.JSON(http.StatusOK, gin.H{"vacancy": vacancy})
}

// DeleteVacancy archives a vacancy (soft delete).
// DELETE /api/recruiter/vacancies/:id
func DeleteVacancy(c *gin.Context) {
	recruiterID := c.GetString("recruiter_id")
	vacancyID := c.Param("id")

	result := database.DB.Model(&models.Vacancy{}).
		Where("id = ? AND recruiter_id = ?", vacancyID, recruiterID).
		Update("status", "archived")

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Vaga não encontrada."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Vaga arquivada."})
}

// PermanentDeleteVacancy permanently deletes a vacancy.
// DELETE /api/recruiter/vacancies/:id/permanent
func PermanentDeleteVacancy(c *gin.Context) {
	recruiterID := c.GetString("recruiter_id")
	vacancyID := c.Param("id")

	result := database.DB.Where("id = ? AND recruiter_id = ?", vacancyID, recruiterID).Delete(&models.Vacancy{})

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Vaga não encontrada."})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Vaga eliminada permanentemente."})
}

// GetPublicVacancies returns all active vacancies (public endpoint for students).
// GET /api/vacancies
// Query params: tag, type, max_experience (0|1|2|3 — max years of experience in description)
func GetPublicVacancies(c *gin.Context) {
	loadVacanciesIfNeeded()

	var result []models.Vacancy
	tagFilter := c.Query("tag")
	typeFilter := c.Query("type")
	maxExpStr := c.Query("max_experience") // e.g. "0", "1", "2", "3"

	vacanciesMutex.RLock()
	defer vacanciesMutex.RUnlock()

	for _, v := range cachedVacancies {
		// Filter by tag
		if tagFilter != "" {
			tagMatch := false
			for _, t := range v.Tags {
				if strings.EqualFold(t, tagFilter) {
					tagMatch = true
					break
				}
			}
			if !tagMatch {
				continue
			}
		}

		// Filter by type
		if typeFilter != "" && v.Type != typeFilter {
			continue
		}

		// Filter by max years of experience (parsed from description)
		if maxExpStr != "" {
			maxExp, err := strconv.Atoi(maxExpStr)
			if err == nil {
				years := extractExperienceYears(v.Description)
				if years > maxExp {
					continue
				}
			}
		}

		result = append(result, v)
	}

	c.JSON(http.StatusOK, gin.H{
		"vacancies": result,
		"count":     len(result),
	})
}

// extractExperienceYears parses the maximum years of experience required from a
// job description. Returns 0 if no experience is required or none is mentioned.
func extractExperienceYears(description string) int {
	if strings.TrimSpace(description) == "" {
		return 0
	}
	d := strings.ToLower(description)

	// Explicit 0 experience / entry-level phrases
	noExpPhrases := []string{
		"sem experiência", "sem experiencia",
		"não é necessária experiência", "não requer experiência",
		"no experience required", "no prior experience", "no experience needed",
		"0+ years", "0 years", "0 anos", "0-0 years", "0-0 anos",
		"entry level", "entry-level",
	}
	for _, p := range noExpPhrases {
		if strings.Contains(d, p) {
			return 0
		}
	}

	patterns := []*regexp.Regexp{
		// 1. Range of years (e.g. "1-2 years", "1–2 years", "1—2 years", "1 to 2 years", "1 a 2 anos", "0-1 years")
		regexp.MustCompile(`(\d+)\s*(?:[-–—]|to|a)\s*(\d+)\s*(?:anos?|years?|yrs?)`),

		// 2. Prefixes like "mínimo 2 anos", "min 2 years", "at least 3 years", "up to 2 years", "até 2 anos"
		regexp.MustCompile(`(?:m[ií]nimo|min\.?|at least|pelo menos|at[ée]|up to|maximum|m[áa]ximo)\s*(\d+)\s*\+?\s*(?:anos?|years?|yrs?)`),

		// 3. Experience context + numbers + years (e.g. "2+ years of experience", "2 anos de experiência", "1 year of experience")
		regexp.MustCompile(`(\d+)\s*\+?\s*(?:anos?|years?|yrs?)\s*(?:de\s+|of\s+)?(?:professional\s+|profissional\s+|effective\s+|efetiva\s+|efectiva\s+)?(?:experi[eê]nci|experienc)`),

		// 4. Experience word followed within ~35 chars by number + years (e.g. "experience (1–2 years)", "experience: 2 years")
		regexp.MustCompile(`(?:experi[eê]nci|experienc)[^\n.]{0,35}?(\d+)\s*\+?\s*(?:anos?|years?|yrs?)`),

		// 5. Direct year requirement with plus: "2+ years", "3+ anos", "2+ yrs"
		regexp.MustCompile(`(\d+)\+\s*(?:anos?|years?|yrs?)`),

		// 6. Generic "X years" / "X anos" near experience terms
		regexp.MustCompile(`(\d+)\s+(?:anos?|years?|yrs?)\s*(?:de\s+|of\s+)?experi`),
	}

	maxFound := -1
	for _, re := range patterns {
		matches := re.FindAllStringSubmatch(d, -1)
		for _, m := range matches {
			if len(m) > 1 {
				a, _ := strconv.Atoi(m[1])
				b := a
				if len(m) > 2 && m[2] != "" {
					b, _ = strconv.Atoi(m[2])
				}
				v := a
				if b > v {
					v = b
				}
				if v > maxFound {
					maxFound = v
				}
			}
		}
	}

	if maxFound == -1 {
		return 0 // no mention → treat as entry-level
	}
	return maxFound
}


// GetPublicVacancy returns a single public vacancy by ID.
// GET /api/vacancies/:id
func GetPublicVacancy(c *gin.Context) {
	vacancyID := c.Param("id")

	loadVacanciesIfNeeded()
	vacanciesMutex.RLock()
	defer vacanciesMutex.RUnlock()

	for _, v := range cachedVacancies {
		if v.ID == vacancyID {
			c.JSON(http.StatusOK, gin.H{
				"vacancy": v,
			})
			return
		}
	}

	c.JSON(http.StatusNotFound, gin.H{"error": "Vaga não encontrada."})
}

// ToggleFavoriteVacancy adds or removes a vacancy from user's favorites.
// Max 10 favorite additions allowed per user per day.
// POST /api/vacancies/:id/favorite
func ToggleFavoriteVacancy(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")
	vacancyID := c.Param("id")

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var existing models.UserVacancyFavorite
	err := database.DB.Where("user_id = ? AND vacancy_id = ?", user.ID, vacancyID).First(&existing).Error

	if err == nil {
		// Favorite exists -> Remove favorite
		if err := database.DB.Delete(&existing).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao remover favorito."})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"is_favorite": false,
			"message":     "Vaga removida dos favoritos.",
		})
		return
	}

	// Favorite does NOT exist -> Add favorite, check 10/day limit
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var countToday int64
	database.DB.Model(&models.UserVacancyFavorite{}).
		Where("user_id = ? AND created_at >= ?", user.ID, startOfDay).
		Count(&countToday)

	if countToday >= 10 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":         "Atingiste o limite máximo de 10 vagas favoritas por dia.",
			"limit_reached": true,
		})
		return
	}

	fav := models.UserVacancyFavorite{
		UserID:    user.ID,
		VacancyID: vacancyID,
		CreatedAt: now,
	}

	if err := database.DB.Create(&fav).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao guardar favorito."})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"is_favorite":         true,
		"message":             "Vaga adicionada aos favoritos.",
		"favorites_count_today": countToday + 1,
	})
}

// GetMyFavoriteVacancies returns all vacancies favorited by the authenticated user.
// GET /api/vacancies/favorites/me
func GetMyFavoriteVacancies(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var favs []models.UserVacancyFavorite
	if err := database.DB.Where("user_id = ?", user.ID).Order("created_at DESC").Find(&favs).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao carregar favoritos."})
		return
	}

	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	var countToday int64
	database.DB.Model(&models.UserVacancyFavorite{}).
		Where("user_id = ? AND created_at >= ?", user.ID, startOfDay).
		Count(&countToday)

	loadVacanciesIfNeeded()
	vacanciesMutex.RLock()
	vacMap := make(map[string]models.Vacancy)
	for _, v := range cachedVacancies {
		vacMap[v.ID] = v
	}
	vacanciesMutex.RUnlock()

	var apps []models.VacancyApplication
	database.DB.Where("user_id = ?", user.ID).Find(&apps)
	appMap := make(map[string]models.VacancyApplication)
	for _, a := range apps {
		appMap[a.VacancyID] = a
	}

	type FavoriteItem struct {
		models.Vacancy
		IsFavorite        bool       `json:"is_favorite"`
		Applied           bool       `json:"applied"`
		ApplicationStatus string     `json:"application_status,omitempty"`
		AppliedAt         *time.Time `json:"applied_at,omitempty"`
		FavoritedAt       time.Time  `json:"favorited_at"`
	}

	var result []FavoriteItem
	for _, fav := range favs {
		v, exists := vacMap[fav.VacancyID]
		if !exists {
			var dbVac models.Vacancy
			if err := database.DB.Preload("Recruiter").Where("id = ?", fav.VacancyID).First(&dbVac).Error; err == nil {
				v = dbVac
			} else {
				continue
			}
		}

		item := FavoriteItem{
			Vacancy:     v,
			IsFavorite:  true,
			FavoritedAt: fav.CreatedAt,
		}

		if app, hasApp := appMap[fav.VacancyID]; hasApp {
			item.Applied = true
			item.ApplicationStatus = app.Status
			item.AppliedAt = &app.AppliedAt
		}

		result = append(result, item)
	}

	c.JSON(http.StatusOK, gin.H{
		"vacancies":             result,
		"count":                 len(result),
		"favorites_count_today": countToday,
	})
}

// GetMyVacancyApplications returns all vacancies to which the authenticated user has applied.
// GET /api/vacancies/applications/me
func GetMyVacancyApplications(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var apps []models.VacancyApplication
	if err := database.DB.Where("user_id = ?", user.ID).Order("applied_at DESC").Find(&apps).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao carregar candidaturas."})
		return
	}

	var favs []models.UserVacancyFavorite
	database.DB.Where("user_id = ?", user.ID).Find(&favs)
	favMap := make(map[string]bool)
	for _, f := range favs {
		favMap[f.VacancyID] = true
	}

	loadVacanciesIfNeeded()
	vacanciesMutex.RLock()
	vacMap := make(map[string]models.Vacancy)
	for _, v := range cachedVacancies {
		vacMap[v.ID] = v
	}
	vacanciesMutex.RUnlock()

	type ApplicationItem struct {
		models.Vacancy
		IsFavorite        bool      `json:"is_favorite"`
		Applied           bool      `json:"applied"`
		ApplicationStatus string    `json:"application_status"`
		AppliedAt         time.Time `json:"applied_at"`
	}

	var result []ApplicationItem
	for _, app := range apps {
		v, exists := vacMap[app.VacancyID]
		if !exists {
			var dbVac models.Vacancy
			if err := database.DB.Preload("Recruiter").Where("id = ?", app.VacancyID).First(&dbVac).Error; err == nil {
				v = dbVac
			} else {
				continue
			}
		}

		item := ApplicationItem{
			Vacancy:           v,
			IsFavorite:        favMap[app.VacancyID],
			Applied:           true,
			ApplicationStatus: app.Status,
			AppliedAt:         app.AppliedAt,
		}

		result = append(result, item)
	}

	c.JSON(http.StatusOK, gin.H{
		"vacancies": result,
		"count":     len(result),
	})
}

// ApplyToVacancy records that a user applied to a vacancy and schedules a 1-week follow-up email.
// POST /api/vacancies/:id/apply
func ApplyToVacancy(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")
	vacancyID := c.Param("id")

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	now := time.Now()
	followupDate := now.Add(7 * 24 * time.Hour)

	var app models.VacancyApplication
	err := database.DB.Where("user_id = ? AND vacancy_id = ?", user.ID, vacancyID).First(&app).Error

	if err == nil {
		app.AppliedAt = now
		app.FollowupEmailSent = false
		app.FollowupEmailDate = followupDate
		if app.Status == "" {
			app.Status = "pending"
		}
		database.DB.Save(&app)
	} else {
		app = models.VacancyApplication{
			UserID:            user.ID,
			VacancyID:         vacancyID,
			AppliedAt:         now,
			FollowupEmailSent: false,
			FollowupEmailDate: followupDate,
			Status:            "pending",
		}
		if err := database.DB.Create(&app).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao registar candidatura."})
			return
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "Candidatura registada com sucesso! Em 1 semana enviaremos um email de acompanhamento para sabermos se foste aceite.",
		"application": app,
	})
}

// RemoveApplication removes a user's application.
// DELETE /api/vacancies/:id/apply
func RemoveApplication(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")
	vacancyID := c.Param("id")

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	if err := database.DB.Where("user_id = ? AND vacancy_id = ?", user.ID, vacancyID).Delete(&models.VacancyApplication{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Erro ao remover candidatura."})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Candidatura removida com sucesso.",
	})
}

// UpdateApplicationStatus updates status of a user's application (e.g. accepted / rejected / ignored).
// POST /api/vacancies/:id/application-status
func UpdateApplicationStatus(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")
	vacancyID := c.Param("id")

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var input struct {
		Status           string `json:"status" binding:"required"`
		ResponseTimeDays *int   `json:"response_time_days"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Dados inválidos."})
		return
	}

	var app models.VacancyApplication
	if err := database.DB.Where("user_id = ? AND vacancy_id = ?", user.ID, vacancyID).First(&app).Error; err != nil {
		app = models.VacancyApplication{
			UserID:            user.ID,
			VacancyID:         vacancyID,
			AppliedAt:         time.Now(),
			FollowupEmailSent: true,
			Status:            input.Status,
		}
	} else {
		app.Status = input.Status
	}

	if input.ResponseTimeDays != nil {
		app.ResponseTimeDays = input.ResponseTimeDays
	} else if app.ResponseTimeDays == nil && (input.Status == "accepted" || input.Status == "rejected" || input.Status == "ignored") {
		days := int(time.Since(app.AppliedAt).Hours() / 24)
		if days < 1 {
			days = 1
		}
		app.ResponseTimeDays = &days
	}

	database.DB.Save(&app)
	c.JSON(http.StatusOK, gin.H{
		"message":     "Estado da candidatura atualizado.",
		"application": app,
	})
}

// GetCommunityVacancyStats returns global and per-vacancy stats (rejection rates, response times, favorites).
// GET /api/vacancies/community-stats
func GetCommunityVacancyStats(c *gin.Context) {
	var totalFavorites int64
	database.DB.Model(&models.UserVacancyFavorite{}).Count(&totalFavorites)

	var totalApps int64
	database.DB.Model(&models.VacancyApplication{}).Count(&totalApps)

	var totalRejections int64
	database.DB.Model(&models.VacancyApplication{}).Where("status = ?", "rejected").Count(&totalRejections)

	var totalAccepted int64
	database.DB.Model(&models.VacancyApplication{}).Where("status = ?", "accepted").Count(&totalAccepted)

	rejectionRate := 0.0
	if totalApps > 0 {
		rejectionRate = (float64(totalRejections) / float64(totalApps)) * 100.0
	}

	var avgResponseDays float64
	row := database.DB.Model(&models.VacancyApplication{}).
		Where("response_time_days IS NOT NULL AND response_time_days > 0").
		Select("COALESCE(AVG(response_time_days), 0)").
		Row()
	_ = row.Scan(&avgResponseDays)
	if avgResponseDays == 0 {
		avgResponseDays = 4.5
	}

	type FavCountResult struct {
		VacancyID string
		Count     int
	}
	var topFavResults []FavCountResult
	database.DB.Model(&models.UserVacancyFavorite{}).
		Select("vacancy_id, COUNT(*) as count").
		Group("vacancy_id").
		Order("count DESC").
		Limit(10).
		Scan(&topFavResults)

	loadVacanciesIfNeeded()
	vacanciesMutex.RLock()
	vacMap := make(map[string]models.Vacancy)
	for _, v := range cachedVacancies {
		vacMap[v.ID] = v
	}
	vacanciesMutex.RUnlock()

	type CommunityVacancyStatItem struct {
		models.Vacancy
		FavoritesCount    int     `json:"favorites_count"`
		ApplicationsCount int     `json:"applications_count"`
		RejectionsCount   int     `json:"rejections_count"`
		AcceptedCount     int     `json:"accepted_count"`
		RejectionRate     float64 `json:"rejection_rate"`
		AvgResponseDays   float64 `json:"avg_response_days"`
	}

	var topCommunityVacancies []CommunityVacancyStatItem

	for _, item := range topFavResults {
		v, exists := vacMap[item.VacancyID]
		if !exists {
			var dbVac models.Vacancy
			if err := database.DB.Preload("Recruiter").Where("id = ?", item.VacancyID).First(&dbVac).Error; err == nil {
				v = dbVac
			} else {
				continue
			}
		}

		var appCount int64
		var rejCount int64
		var accCount int64
		database.DB.Model(&models.VacancyApplication{}).Where("vacancy_id = ?", item.VacancyID).Count(&appCount)
		database.DB.Model(&models.VacancyApplication{}).Where("vacancy_id = ? AND status = ?", item.VacancyID, "rejected").Count(&rejCount)
		database.DB.Model(&models.VacancyApplication{}).Where("vacancy_id = ? AND status = ?", item.VacancyID, "accepted").Count(&accCount)

		var vAvgResp float64
		r := database.DB.Model(&models.VacancyApplication{}).
			Where("vacancy_id = ? AND response_time_days IS NOT NULL", item.VacancyID).
			Select("COALESCE(AVG(response_time_days), 0)").
			Row()
		_ = r.Scan(&vAvgResp)
		if vAvgResp == 0 {
			vAvgResp = avgResponseDays
		}

		vRejRate := 0.0
		if appCount > 0 {
			vRejRate = (float64(rejCount) / float64(appCount)) * 100.0
		} else {
			vRejRate = 18.5
		}

		topCommunityVacancies = append(topCommunityVacancies, CommunityVacancyStatItem{
			Vacancy:           v,
			FavoritesCount:    item.Count,
			ApplicationsCount: int(appCount),
			RejectionsCount:   int(rejCount),
			AcceptedCount:     int(accCount),
			RejectionRate:     vRejRate,
			AvgResponseDays:   vAvgResp,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"total_favorites":         totalFavorites,
		"total_applications":      totalApps,
		"total_rejections":        totalRejections,
		"total_accepted":          totalAccepted,
		"rejection_rate_percent":  rejectionRate,
		"avg_response_days":       avgResponseDays,
		"top_community_vacancies": topCommunityVacancies,
	})
}

