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
	d := strings.ToLower(description)

	noExpPhrases := []string{
		"sem experiência", "sem experiencia",
		"não é necessária experiência", "não requer experiência",
		"no experience required", "no prior experience",
	}
	for _, p := range noExpPhrases {
		if strings.Contains(d, p) {
			return 0
		}
	}

	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(\d+)\s*[-–a]\s*(\d+)\s*anos?\s*de\s*experi`), // "1-2 anos de experiência"
		regexp.MustCompile(`m[ií]nimo\s+(\d+)\s+anos?`),                    // "mínimo 2 anos"
		regexp.MustCompile(`at[ée]\s+(\d+)\s+anos?`),                       // "até 2 anos"
		regexp.MustCompile(`(\d+)\+\s*anos?`),                              // "2+ anos"
		regexp.MustCompile(`(\d+)\s+anos?\s*de\s*experi`),                  // "2 anos de experiência"
		regexp.MustCompile(`(\d+)\s+ano\s*de\s*experi`),                    // "1 ano de experiência"
		regexp.MustCompile(`(\d+)\s+years?\s*of\s*experience`),             // "2 years of experience"
	}

	maxFound := -1
	for _, re := range patterns {
		m := re.FindStringSubmatch(d)
		if m != nil {
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
