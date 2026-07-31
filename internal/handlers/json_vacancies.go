package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/paiva/SkillBridge/Backend/internal/jobs"
	"github.com/paiva/SkillBridge/Backend/internal/models"
	"github.com/paiva/SkillBridge/Backend/internal/storage"
)

var (
	cachedVacancies []models.Vacancy
	vacanciesMutex  sync.RWMutex
	vacanciesLoaded bool
	loadedVersion   uint64
)

type JsonVacancy struct {
	CompanyName       string   `json:"company_name"`
	CompanyUrl        string   `json:"company_url"`
	CompanyProfileUrl string   `json:"company_profile_url"`
	Title             string   `json:"title"`
	Type              string   `json:"type"`
	Tags              []string `json:"tags"`
	Description       string   `json:"description"`
	ApplicationUrl    string   `json:"application_url"`
	Region            string   `json:"region"`
	WorkMode          string   `json:"work_mode"`
	EmploymentType    string   `json:"employment_type"`
	PublishedAt       string   `json:"published_at"`
	LogoUrl           string   `json:"logo_url"`
}

func loadVacanciesIfNeeded() {
	vacanciesMutex.Lock()
	defer vacanciesMutex.Unlock()

	currentVersion := jobs.CacheVersion.Load()

	// If cache is valid and version hasn't changed, nothing to do.
	if vacanciesLoaded && currentVersion == loadedVersion {
		return
	}

	var jsonVacs []JsonVacancy
	var defaultPubDate time.Time

	// 1. Try GCS (primary — survives Cloud Run restarts)
	if storage.GCSClient != nil {
		data, err := storage.ReadObject("data/vagas_final.json")
		if err == nil {
			if jsonErr := json.Unmarshal(data, &jsonVacs); jsonErr == nil {
				defaultPubDate = time.Now()
				log.Printf("[vacancies] Loaded %d vacancies from GCS", len(jsonVacs))
				goto build
			} else {
				log.Printf("[vacancies] Error decoding GCS vagas_final.json: %v", jsonErr)
			}
		} else {
			log.Printf("[vacancies] GCS read failed, falling back to local file: %v", err)
		}
	}

	// 2. Fallback: local filesystem (dev environment / first deploy seed)
	{
		fileInfo, err := os.Stat("config/vagas_final.json")
		if err != nil {
			log.Printf("[vacancies] Error stat vagas_final.json: %v", err)
			return
		}
		defaultPubDate = fileInfo.ModTime()

		file, err := os.Open("config/vagas_final.json")
		if err != nil {
			log.Printf("[vacancies] Error opening vagas_final.json: %v", err)
			return
		}
		defer file.Close()

		if err := json.NewDecoder(file).Decode(&jsonVacs); err != nil {
			log.Printf("[vacancies] Error decoding vagas_final.json: %v", err)
			return
		}
		log.Printf("[vacancies] Loaded %d vacancies from local file", len(jsonVacs))
	}

build:
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
				fmt.Printf("[vacancies] Error parsing published_at for %s: %v\n", jv.Title, err)
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
				LogoURL: func() string {
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
	loadedVersion = currentVersion
}
