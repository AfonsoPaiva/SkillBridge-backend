package handlers

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
	CompanyUrl        string   `json:"company_url"`
	CompanyProfileUrl string   `json:"company_profile_url"`
	Title             string   `json:"title"`
	Type           string   `json:"type"`
	Tags           []string `json:"tags"`
	Description    string   `json:"description"`
	ApplicationUrl string   `json:"application_url"`
	Region         string   `json:"region"`
	WorkMode       string   `json:"work_mode"`
	EmploymentType string   `json:"employment_type"`
	PublishedAt    string   `json:"published_at"`
	LogoUrl        string   `json:"logo_url"`
}

func loadVacanciesIfNeeded() {
	vacanciesMutex.Lock()
	defer vacanciesMutex.Unlock()
	if vacanciesLoaded {
		return
	}

	fileInfo, err := os.Stat("config/vagas_final.json")
	var defaultPubDate time.Time
	if err == nil {
		defaultPubDate = fileInfo.ModTime()
	} else {
		defaultPubDate = time.Now()
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
	for i, jv := range jsonVacs {
		pubDate := defaultPubDate
		if jv.PublishedAt != "" {
			parsedDate, err := time.Parse("2006-01-02", jv.PublishedAt)
			if err != nil {
				parsedDate, err = time.Parse(time.RFC3339, jv.PublishedAt)
			}
			if err == nil {
				pubDate = parsedDate
			} else {
				fmt.Printf("Error parsing published_at date for vacancy %s: %v\n", jv.Title, err)
			}
		}

		cachedVacancies = append(cachedVacancies, models.Vacancy{
			ID:             fmt.Sprintf("json-vac-%d", i),
			Title:          jv.Title,
			Description:    jv.Description,
			Type:           jv.Type,
			Tags:           jv.Tags,
			Status:         "active",
			PublishedAt:    pubDate,
			ExpiresAt:      pubDate.AddDate(1, 0, 0),
			RecruiterID:    "json-recruiter",
			Recruiter: models.Recruiter{
				CompanyName:       jv.CompanyName,
				CompanyURL:        jv.CompanyUrl,
				CompanyProfileURL: jv.CompanyProfileUrl,
				LogoURL:     func() string {
					if jv.LogoUrl != "" {
						return jv.LogoUrl
					}
					domain := jv.CompanyUrl
					if domain == "" {
						domain = strings.ToLower(strings.ReplaceAll(jv.CompanyName, " ", "")) + ".com"
					} else {
						domain = strings.TrimPrefix(domain, "http://")
						domain = strings.TrimPrefix(domain, "https://")
						domain = strings.TrimPrefix(domain, "www.")
						if idx := strings.Index(domain, "/"); idx != -1 {
							domain = domain[:idx]
						}
					}
					return "https://t1.gstatic.com/faviconV2?client=SOCIAL&type=FAVICON&fallback_opts=TYPE,SIZE,URL&url=http://" + domain + "&size=128"
				}(),
			},
			Region:         jv.Region,
			WorkMode:       jv.WorkMode,
			EmploymentType: jv.EmploymentType,
			ApplicationURL: jv.ApplicationUrl,
		})
	}
	vacanciesLoaded = true
}
