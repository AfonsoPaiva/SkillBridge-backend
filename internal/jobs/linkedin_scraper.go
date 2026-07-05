package jobs

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
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

// StartLinkedInScraperJob initializes a job that scrapes LinkedIn every 4 weeks
func StartLinkedInScraperJob() {
	// 4 weeks = 28 days
	ticker := time.NewTicker(28 * 24 * time.Hour)
	log.Println("[jobs] LinkedIn scraper job started (runs every 4 weeks)")

	go func() {
		// Run on startup after a small delay
		time.Sleep(30 * time.Second)
		
		path := filepath.Join("config", "vagas_final.json")
		info, err := os.Stat(path)
		shouldRun := true
		if err == nil {
			if time.Since(info.ModTime()) < 28*24*time.Hour {
				log.Printf("[jobs] vagas_final.json was updated %v ago, skipping scrape.", time.Since(info.ModTime()).Round(time.Hour))
				shouldRun = false
			}
		}

		if shouldRun {
			ScrapeLinkedInJobs()
		}

		for range ticker.C {
			ScrapeLinkedInJobs()
		}
	}()
}

func loadAllSkills() []string {
	var allSkills []string
	
	path := filepath.Join("config", "skills.json")
	file, err := os.Open(path)
	if err != nil {
		log.Printf("[jobs] Error opening skills.json: %v", err)
		return []string{"Trabalho em Equipa", "Comunicação"}
	}
	defer file.Close()
	
	var data struct {
		Sections []struct {
			Skills []string `json:"skills"`
		} `json:"sections"`
	}
	
	if err := json.NewDecoder(file).Decode(&data); err != nil {
		log.Printf("[jobs] Error decoding skills.json: %v", err)
		return []string{"Trabalho em Equipa", "Comunicação"}
	}
	
	for _, section := range data.Sections {
		allSkills = append(allSkills, section.Skills...)
	}
	
	return allSkills
}

func ScrapeLinkedInJobs() {
	log.Println("[jobs] Starting LinkedIn job scraper...")

	allSkills := loadAllSkills()
	var vacancies []JsonVacancy
	seenJobs := make(map[string]bool)
	client := &http.Client{Timeout: 30 * time.Second}

	for start := 0; start < 1000; start += 25 {
		// sortBy=R garante que a pesquisa é por "Relevância", que favorece empresas maiores e com mais engagement.
		url := fmt.Sprintf("https://www.linkedin.com/jobs-guest/jobs/api/seeMoreJobPostings/search?f_E=1,2,3&geoId=100364837&location=Portugal&sortBy=R&start=%d", start)
		
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			log.Printf("[jobs] Error creating request: %v", err)
			break
		}

		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/115.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
		req.Header.Set("Accept-Language", "en-US,en;q=0.5")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[jobs] Error fetching LinkedIn jobs: %v", err)
			break
		}

		if resp.StatusCode != 200 {
			log.Printf("[jobs] Failed to fetch LinkedIn jobs, status code: %d", resp.StatusCode)
			resp.Body.Close()
			break
		}

		doc, err := goquery.NewDocumentFromReader(resp.Body)
		resp.Body.Close()

		if err != nil {
			log.Printf("[jobs] Error parsing HTML: %v", err)
			break
		}

		jobsFound := 0
		doc.Find("div.job-search-card").Each(func(i int, s *goquery.Selection) {
			title := strings.TrimSpace(s.Find("h3.base-search-card__title").Text())
			company := strings.TrimSpace(s.Find("h4.base-search-card__subtitle").Text())
			companyUrl, _ := s.Find("h4.base-search-card__subtitle a").Attr("href")
			location := strings.TrimSpace(s.Find("span.job-search-card__location").Text())
			jobLink, _ := s.Find("a.base-card__full-link").Attr("href")
			logoUrl, _ := s.Find("img.artdeco-entity-image").Attr("data-delayed-url")
			
			if logoUrl == "" {
				logoUrl, _ = s.Find("img.artdeco-entity-image").Attr("src")
			}

			if idx := strings.Index(jobLink, "?"); idx != -1 {
				jobLink = jobLink[:idx]
			}
			
			if idx := strings.Index(companyUrl, "?"); idx != -1 {
				companyUrl = companyUrl[:idx]
			}

			if title != "" && company != "" {
				// Prevent duplicates
				if seenJobs[jobLink] {
					return // continue to next element in .Each
				}
				seenJobs[jobLink] = true

				desc := fetchJobDescription(jobLink)
				time.Sleep(500 * time.Millisecond) // Prevent rate limiting from LinkedIn

				vacancies = append(vacancies, JsonVacancy{
					CompanyName:       company,
					CompanyUrl:        companyUrl,
					CompanyProfileUrl: companyUrl,
					Title:             title,
					Region:            location,
					ApplicationUrl:    jobLink,
					Type:              "junior_position",
					Tags:              extractSkills(title, desc, allSkills),
					Description:       desc,
					WorkMode:          "Hybrid",
					EmploymentType:    "Full-time",
					PublishedAt:       time.Now().Format("2006-01-02"),
					LogoUrl:           logoUrl,
				})
				jobsFound++
			}
		})

		if jobsFound == 0 {
			break
		}

		time.Sleep(2 * time.Second)
	}

	if len(vacancies) > 0 {
		log.Printf("[jobs] Scraped %d jobs successfully", len(vacancies))
		saveVacanciesToJson(vacancies)
	} else {
		log.Println("[jobs] No jobs scraped")
	}
}

func saveVacanciesToJson(vacancies []JsonVacancy) {
	path := filepath.Join("config", "vagas_final.json")

	if err := os.MkdirAll("config", 0755); err != nil {
		log.Printf("[jobs] Error creating config dir: %v", err)
		return
	}

	file, err := os.Create(path)
	if err != nil {
		log.Printf("[jobs] Error saving vacancies JSON: %v", err)
		return
	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false) // Previne que o "&" seja convertido para "\u0026"

	if err := encoder.Encode(vacancies); err != nil {
		log.Printf("[jobs] Error encoding vacancies JSON: %v", err)
	} else {
		log.Println("[jobs] vagas_final.json updated successfully")
	}
}

func containsExactWord(text, word string) bool {
	if word == "" {
		return false
	}
	textLower := strings.ToLower(text)
	wordLower := strings.ToLower(word)

	idx := 0
	for {
		i := strings.Index(textLower[idx:], wordLower)
		if i == -1 {
			return false
		}

		actualIdx := idx + i

		beforeOK := true
		if actualIdx > 0 {
			r, _ := utf8.DecodeLastRuneInString(textLower[:actualIdx])
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				beforeOK = false
			}
		}

		afterOK := true
		afterIdx := actualIdx + len(wordLower)
		if afterIdx < len(textLower) {
			r, _ := utf8.DecodeRuneInString(textLower[afterIdx:])
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				afterOK = false
			}
		}

		if beforeOK && afterOK {
			return true
		}

		idx = actualIdx + len(wordLower)
	}
}

func extractSkills(title, description string, allSkills []string) []string {
	combinedText := title + " " + description
	var skills []string
	skillSet := make(map[string]bool)

	// Direct match with the extensive list
	for _, skill := range allSkills {
		if containsExactWord(combinedText, skill) {
			if !skillSet[skill] {
				skills = append(skills, skill)
				skillSet[skill] = true
			}
		}
	}

	// Keywords mapping
	keywords := map[string]string{
		"software":         "Software Engineering",
		"developer":        "Desenvolvimento de Software",
		"engenheir":        "Engenharia",
		"dados":            "Análise de Dados",
		"data":             "Data Science",
		"marketing":        "Marketing",
		"design":           "Design Gráfico",
		"gestão":           "Gestão",
		"finanç":           "Finanças",
		"rh":               "Gestão de Recursos Humanos",
		"recursos humanos": "Gestão de Recursos Humanos",
		"comunica":         "Comunicação",
		"vendas":           "Vendas",
		"it ":              "Tecnologias de Informação",
		"frontend":         "Frontend",
		"backend":          "Backend",
		"fullstack":        "Fullstack",
		"web":              "Desenvolvimento Web",
		"cloud":            "Cloud Computing",
		"segurança":        "Cibersegurança",
		"security":         "Cibersegurança",
		"arquitetura":      "Arquitetura",
		"civil":            "Engenharia Civil",
		"mecânica":         "Engenharia Mecânica",
		"eletrotécnica":    "Engenharia Eletrotécnica",
		"administrativ":    "Administração",
		"rececionista":     "Atendimento ao Cliente",
		"recepcionista":    "Atendimento ao Cliente",
		"educador":         "Educação",
	}

	for kw, mappedSkill := range keywords {
		if containsExactWord(combinedText, kw) {
			if !skillSet[mappedSkill] {
				skills = append(skills, mappedSkill)
				skillSet[mappedSkill] = true
			}
		}
	}

	if len(skills) == 0 {
		skills = append(skills, "Trabalho em Equipa", "Comunicação")
	}

	return skills
}

func fetchJobDescription(jobUrl string) string {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", jobUrl, nil)
	if err != nil {
		return "Para mais detalhes, visite o link da candidatura."
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/115.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return "Para mais detalhes, visite o link da candidatura."
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "Para mais detalhes, visite o link da candidatura."
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "Para mais detalhes, visite o link da candidatura."
	}

	// Try multiple selectors where LinkedIn might put the description
	selection := doc.Find("div.show-more-less-html__markup")
	if selection.Length() == 0 {
		selection = doc.Find("div.description__text")
	}

	if selection.Length() > 0 {
		// Clean up formatting
		selection.Find("br").ReplaceWithHtml("\n")
		selection.Find("p").Each(func(i int, s *goquery.Selection) {
			s.AppendHtml("\n\n")
		})
		selection.Find("li").Each(func(i int, s *goquery.Selection) {
			s.PrependHtml("• ")
			s.AppendHtml("\n")
		})

		text := selection.Text()
		
		// Remove excessive newlines and leading/trailing spaces
		text = regexp.MustCompile(`\n{3,}`).ReplaceAllString(text, "\n\n")
		text = strings.TrimSpace(text)

		if text != "" {
			return text
		}
	}

	return "Para mais detalhes, visite o link da candidatura."
}
