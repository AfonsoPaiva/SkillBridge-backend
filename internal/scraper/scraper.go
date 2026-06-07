package scraper

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/models"
)

var juniorKeywords = []string{
	"junior", "júnior", "jr.", "jr ",
	"intern", "internship", "estágio", "estagio", "estágiário", "estagiário",
	"summer", "verão", "verao",
	"trainee", "graduate", "entry level", "entry-level",
}

// RunScraper executes the scraping process for all configured job boards
func RunScraper() {
	log.Println("Starting job scraper...")

	greenhouseCompanies := []string{"cloudflare", "feedzai", "remote", "outsystems", "talkdesk"}
	leverCompanies := []string{"swordhealth", "unbabel", "rows"}
	workableCompanies := []string{"criticalmanufacturing", "barkyn", "nutrium", "aptoide", "codacy"}

	for _, company := range greenhouseCompanies {
		scrapeGreenhouse(company)
	}

	for _, company := range leverCompanies {
		scrapeLever(company)
	}

	for _, company := range workableCompanies {
		scrapeWorkable(company)
	}

	scrapeArbeitnow()

	log.Println("Scraping finished.")
}

func classifyVacancyType(titleLower, contentLower string) string {
	if strings.Contains(titleLower, "summer") || strings.Contains(titleLower, "verão") || strings.Contains(titleLower, "verao") {
		return "summer_internship"
	}
	if strings.Contains(titleLower, "intern") || strings.Contains(titleLower, "estágio") ||
		strings.Contains(titleLower, "estagio") || strings.Contains(titleLower, "trainee") ||
		strings.Contains(titleLower, "estagiário") || strings.Contains(titleLower, "estágiário") {
		if strings.Contains(contentLower, "extracurricular") || strings.Contains(titleLower, "extracurricular") {
			return "extracurricular_internship"
		}
		if strings.Contains(contentLower, "curricular") || strings.Contains(titleLower, "curricular") {
			return "curricular_internship"
		}
		return "summer_internship"
	}
	return "junior_position"
}

func isPortugalLocation(region string) bool {
	if region == "" {
		return true
	}
	regionLower := strings.ToLower(region)
	return strings.Contains(regionLower, "portugal") ||
		strings.Contains(regionLower, ", pt") ||
		strings.Contains(regionLower, " pt") ||
		strings.Contains(regionLower, "porto") ||
		strings.Contains(regionLower, "lisboa") ||
		strings.Contains(regionLower, "lisbon") ||
		strings.Contains(regionLower, "maia") ||
		strings.Contains(regionLower, "braga") ||
		strings.Contains(regionLower, "coimbra") ||
		strings.Contains(regionLower, "aveiro") ||
		strings.Contains(regionLower, "faro") ||
		strings.Contains(regionLower, "funchal") ||
		strings.Contains(regionLower, "leiria") ||
		strings.Contains(regionLower, "setúbal") ||
		strings.Contains(regionLower, "setubal")
}

func isJobRelevant(title, desc string) bool {
	titleLower := strings.ToLower(title)
	descLower := strings.ToLower(desc)
	for _, kw := range juniorKeywords {
		if strings.Contains(titleLower, kw) || strings.Contains(descLower, kw) {
			return true
		}
	}
	return false
}

func getOrCreateRecruiter(companyName, domain string) string {
	var recruiter models.Recruiter
	result := database.DB.Where("company_name = ?", companyName).First(&recruiter)
	if result.Error != nil {
		logoURL := ""
		if domain != "" {
			logoURL = "https://www.google.com/s2/favicons?domain=" + domain + "&sz=128"
		}
		recruiter = models.Recruiter{
			FullName:           "Admin Auto-Scraper",
			CompanyName:        companyName,
			Email:              strings.ToLower(strings.ReplaceAll(companyName, " ", "")) + "@auto-scraped.com",
			CompanyURL:         "https://" + domain,
			LogoURL:            logoURL,
			Status:             "approved",
			CreatedAt:          time.Now(),
		}
		database.DB.Create(&recruiter)
	}
	return recruiter.ID
}

func saveVacancy(recruiterID, title, desc, url, region, workMode, empType string) {
	if !isJobRelevant(title, desc) || !isPortugalLocation(region) {
		return
	}

	var existing models.Vacancy
	if err := database.DB.Where("application_url = ?", url).First(&existing).Error; err == nil {
		return // already exists
	}

	titleLower := strings.ToLower(title)
	descLower := strings.ToLower(desc)
	vacType := classifyVacancyType(titleLower, descLower)

	now := time.Now()
	vacancy := models.Vacancy{
		RecruiterID:    recruiterID,
		Title:          title,
		Type:           vacType,
		Tags:           models.StringList{},
		Description:    desc,
		ApplicationURL: url,
		Region:         region,
		WorkMode:       workMode,
		EmploymentType: empType,
		Status:         "active",
		PublishedAt:    now,
		ExpiresAt:      now.Add(30 * 24 * time.Hour),
		Views:          0,
	}
	database.DB.Create(&vacancy)
	log.Printf("Inserted vacancy: %s at %s\n", title, url)
}

func scrapeGreenhouse(company string) {
	url := fmt.Sprintf("https://boards-api.greenhouse.io/v1/boards/%s/jobs?content=true", company)
	resp, err := http.Get(url)
	if err != nil {
		log.Printf("Error fetching Greenhouse %s: %v", company, err)
		return
	}
	defer resp.Body.Close()

	var data struct {
		Jobs []struct {
			Title       string `json:"title"`
			AbsoluteURL string `json:"absolute_url"`
			Location    struct {
				Name string `json:"name"`
			} `json:"location"`
			Content string `json:"content"`
		} `json:"jobs"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return
	}

	recruiterID := getOrCreateRecruiter(strings.Title(company), company+".com")
	for _, job := range data.Jobs {
		desc := stripHTML(job.Content)
		saveVacancy(recruiterID, job.Title, desc, job.AbsoluteURL, job.Location.Name, "hybrid", "full_time")
	}
}

func scrapeLever(company string) {
	url := fmt.Sprintf("https://api.lever.co/v0/postings/%s?mode=json", company)
	resp, err := http.Get(url)
	if err != nil {
		log.Printf("Error fetching Lever %s: %v", company, err)
		return
	}
	defer resp.Body.Close()

	var data []struct {
		Text             string `json:"text"`
		HostedURL        string `json:"hostedUrl"`
		DescriptionPlain string `json:"descriptionPlain"`
		Categories       struct {
			Location   string `json:"location"`
			Commitment string `json:"commitment"`
		} `json:"categories"`
		WorkplaceType string `json:"workplaceType"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return
	}

	recruiterID := getOrCreateRecruiter(strings.Title(company), company+".com")
	for _, job := range data {
		empType := "full_time"
		if strings.Contains(strings.ToLower(job.Categories.Commitment), "part") {
			empType = "part_time"
		}
		workMode := "hybrid"
		if strings.Contains(strings.ToLower(job.WorkplaceType), "remote") {
			workMode = "remote"
		}
		saveVacancy(recruiterID, job.Text, job.DescriptionPlain, job.HostedURL, job.Categories.Location, workMode, empType)
	}
}

func scrapeWorkable(company string) {
	url := fmt.Sprintf("https://apply.workable.com/api/v1/widget/accounts/%s", company)
	resp, err := http.Get(url)
	if err != nil {
		log.Printf("Error fetching Workable %s: %v", company, err)
		return
	}
	defer resp.Body.Close()

	var data struct {
		Jobs []struct {
			Title         string `json:"title"`
			URL           string `json:"url"`
			Description   string `json:"description"`
			City          string `json:"city"`
			Country       string `json:"country"`
			Telecommuting bool   `json:"telecommuting"`
		} `json:"jobs"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return
	}

	recruiterID := getOrCreateRecruiter(strings.Title(company), company+".com")
	for _, job := range data.Jobs {
		desc := stripHTML(job.Description)
		region := job.City
		if job.Country != "" {
			region += ", " + job.Country
		}
		workMode := "hybrid"
		if job.Telecommuting {
			workMode = "remote"
		}
		saveVacancy(recruiterID, job.Title, desc, job.URL, region, workMode, "full_time")
	}
}

func scrapeArbeitnow() {
	url := "https://www.arbeitnow.com/api/job-board-api"
	resp, err := http.Get(url)
	if err != nil {
		log.Printf("Error fetching Arbeitnow: %v", err)
		return
	}
	defer resp.Body.Close()

	var data struct {
		Data []struct {
			CompanyName string `json:"company_name"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Location    string `json:"location"`
			Remote      bool   `json:"remote"`
			URL         string `json:"url"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return
	}

	for _, job := range data.Data {
		domain := strings.ToLower(strings.ReplaceAll(job.CompanyName, " ", "")) + ".com"
		recruiterID := getOrCreateRecruiter(job.CompanyName, domain)
		desc := stripHTML(job.Description)
		workMode := "hybrid"
		if job.Remote {
			workMode = "remote"
		}
		saveVacancy(recruiterID, job.Title, desc, job.URL, job.Location, workMode, "full_time")
	}
}

func stripHTML(str string) string {
	re := regexp.MustCompile("<[^>]*>")
	return strings.TrimSpace(re.ReplaceAllString(str, " "))
}
