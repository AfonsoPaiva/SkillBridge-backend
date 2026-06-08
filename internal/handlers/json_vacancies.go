package handlers

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/paiva/SkillBridge/Backend/internal/models"
)

var (
	cachedVacancies []models.Vacancy
	vacanciesMutex  sync.RWMutex
	vacanciesLoaded bool
)

type JsonVacancy struct {
	CompanyName    string   `json:"company_name"`
	Title          string   `json:"title"`
	Type           string   `json:"type"`
	Tags           []string `json:"tags"`
	Description    string   `json:"description"`
	ApplicationUrl string   `json:"application_url"`
	Region         string   `json:"region"`
	WorkMode       string   `json:"work_mode"`
	EmploymentType string   `json:"employment_type"`
}

func loadVacanciesIfNeeded() {
	vacanciesMutex.Lock()
	defer vacanciesMutex.Unlock()
	if vacanciesLoaded {
		return
	}

	file, err := os.Open("config/vagas_final.json")
	if err != nil {
		fmt.Println("Error opening vagas_final.json:", err)
		return
	}
	defer file.Close()

	var jsonVacs []JsonVacancy
	if err := json.NewDecoder(file).Decode(&jsonVacs); err != nil {
		fmt.Println("Error decoding vagas_final.json:", err)
		return
	}

	cachedVacancies = make([]models.Vacancy, 0, len(jsonVacs))
	now := time.Now()
	for i, jv := range jsonVacs {
		cachedVacancies = append(cachedVacancies, models.Vacancy{
			ID:             fmt.Sprintf("json-vac-%d", i),
			Title:          jv.Title,
			Description:    jv.Description,
			Type:           jv.Type,
			Tags:           jv.Tags,
			Status:         "active",
			PublishedAt:    now,
			ExpiresAt:      now.AddDate(1, 0, 0),
			RecruiterID:    "json-recruiter",
			Recruiter:      models.Recruiter{CompanyName: jv.CompanyName},
			Region:         jv.Region,
			WorkMode:       jv.WorkMode,
			EmploymentType: jv.EmploymentType,
			ApplicationURL: jv.ApplicationUrl,
		})
	}
	vacanciesLoaded = true
}
