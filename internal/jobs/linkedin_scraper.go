package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"github.com/paiva/SkillBridge/Backend/internal/storage"
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

// GCSVagasKey is the GCS object name for the scraped vacancies file.
const GCSVagasKey = "data/vagas_final.json"

// CacheVersion is incremented after each successful save so the handlers package
// knows to reload the vacancy list from GCS/disk on the next request.
var CacheVersion atomic.Uint64

// StartLinkedInScraperJob initializes a job that scrapes LinkedIn every 4 weeks
func StartLinkedInScraperJob() {
	// 4 weeks = 28 days
	ticker := time.NewTicker(28 * 24 * time.Hour)
	log.Println("[jobs] LinkedIn scraper job started (runs every 4 weeks)")

	go func() {
		// Run on startup after a small delay
		time.Sleep(30 * time.Second)

		shouldRun := true

		// 1. Prefer GCS: check object metadata for last-modified time.
		if storage.GCSClient != nil {
			attrs, err := storage.GCSClient.Bucket(storage.BucketName()).Object(GCSVagasKey).Attrs(context.Background())
			if err == nil {
				age := time.Since(attrs.Updated)
				if age < 28*24*time.Hour {
					log.Printf("[jobs] GCS vagas_final.json updated %v ago, skipping scrape.", age.Round(time.Hour))
					shouldRun = false
				}
			}
		} else {
			// 2. Fallback: local file mod time (dev environment)
			path := filepath.Join("config", "vagas_final.json")
			info, err := os.Stat(path)
			if err == nil && time.Since(info.ModTime()) < 28*24*time.Hour {
				log.Printf("[jobs] vagas_final.json updated %v ago (local), skipping scrape.", time.Since(info.ModTime()).Round(time.Hour))
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
	log.Println("[jobs] Starting multi-platform job scraper (LinkedIn + Indeed + Net-Empregos + Expresso Emprego + Emprego.pt + Jobsora + Jooble)...")

	allSkills := loadAllSkills()
	globalSeen := make(map[string]bool)
	uniqueByKey := make(map[string]*JsonVacancy)
	var orderedKeys []string

	mergeInto := func(vacs []JsonVacancy) {
		for i := range vacs {
			v := &vacs[i]
			k := v.CompanyName + "|" + v.Title
			if uniqueByKey[k] == nil {
				uniqueByKey[k] = v
				orderedKeys = append(orderedKeys, k)
			}
		}
	}

	// ── 1. LinkedIn ──────────────────────────────────────────────────────────
	linkedinVacs := scrapeLinkedIn(allSkills, globalSeen)
	mergeInto(linkedinVacs)
	log.Printf("[jobs] LinkedIn contributed %d jobs", len(linkedinVacs))

	// ── 2. Indeed Portugal ───────────────────────────────────────────────────
	indeedVacs := ScrapeIndeedJobs(allSkills, globalSeen)
	mergeInto(indeedVacs)
	log.Printf("[jobs] Indeed contributed %d jobs", len(indeedVacs))

	// ── 3. Net-Empregos ──────────────────────────────────────────────────────
	netEmprVacs := ScrapeNetEmpregos(allSkills, globalSeen)
	mergeInto(netEmprVacs)
	log.Printf("[jobs] Net-Empregos contributed %d jobs", len(netEmprVacs))

	// ── 4. Expresso Emprego ──────────────────────────────────────────────────
	expressoVacs := ScrapeExpressoEmprego(allSkills, globalSeen)
	mergeInto(expressoVacs)
	log.Printf("[jobs] Expresso Emprego contributed %d jobs", len(expressoVacs))

	// ── 5. Emprego.pt ────────────────────────────────────────────────────────
	empregoVacs := ScrapeEmpregoJobs(allSkills, globalSeen)
	mergeInto(empregoVacs)
	log.Printf("[jobs] Emprego.pt contributed %d jobs", len(empregoVacs))

	// ── 6. Jobsora Portugal ──────────────────────────────────────────────────
	jobsoraVacs := ScrapeJobsoraJobs(allSkills, globalSeen)
	mergeInto(jobsoraVacs)
	log.Printf("[jobs] Jobsora contributed %d jobs", len(jobsoraVacs))

	// ── 7. Jooble Portugal ───────────────────────────────────────────────────
	joobleVacs := ScrapeJoobleJobs(allSkills, globalSeen)
	mergeInto(joobleVacs)
	log.Printf("[jobs] Jooble contributed %d jobs", len(joobleVacs))

	// ── Assemble final de-duplicated list ─────────────────────────────────────
	var vacancies []JsonVacancy
	for _, k := range orderedKeys {
		vacancies = append(vacancies, *uniqueByKey[k])
	}

	if len(vacancies) > 0 {
		log.Printf("[jobs] Total unique jobs scraped across all platforms: %d", len(vacancies))
		saveVacanciesToJson(vacancies)
	} else {
		log.Println("[jobs] No jobs scraped from any platform")
	}
}


// linkedInSearchPass fetches one paginated LinkedIn search and merges results
// into uniqueJobs/orderedKeys/seenJobs. Returns the number of new jobs added.
// searchLabel is used only for logging.
func linkedInSearchPass(
	client *http.Client,
	rawURLTemplate string, // must contain one %d placeholder for start offset
	searchLabel string,
	allSkills []string,
	seenJobs map[string]bool,
	uniqueJobs map[string]*JsonVacancy,
	orderedKeys *[]string,
) int {
	added := 0
	for start := 0; start < 500; start += 25 {
		rawURL := fmt.Sprintf(rawURLTemplate, start)

		req, err := http.NewRequest("GET", rawURL, nil)
		if err != nil {
			log.Printf("[jobs][linkedin][%s] Error creating request: %v", searchLabel, err)
			break
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
		req.Header.Set("Accept-Language", "pt-PT,pt;q=0.9,en-US;q=0.8,en;q=0.7")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[jobs][linkedin][%s] Error fetching jobs: %v", searchLabel, err)
			break
		}
		if resp.StatusCode != 200 {
			log.Printf("[jobs][linkedin][%s] Status %d, stopping pass.", searchLabel, resp.StatusCode)
			resp.Body.Close()
			break
		}

		doc, err := goquery.NewDocumentFromReader(resp.Body)
		resp.Body.Close()
		if err != nil {
			log.Printf("[jobs][linkedin][%s] Error parsing HTML: %v", searchLabel, err)
			break
		}

		pageFound := 0
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

			if title == "" || company == "" {
				return
			}

			// De-duplicate by URL across all passes
			if seenJobs[jobLink] {
				return
			}
			seenJobs[jobLink] = true

			key := company + "|" + title
			if existingJob, exists := uniqueJobs[key]; exists {
				if !strings.Contains(existingJob.Region, location) {
					existingJob.Region += " / " + location
				}
				return
			}

			publishedAt, exists := s.Find("time.job-search-card__listdate").Attr("datetime")
			if !exists {
				publishedAt, _ = s.Find("time.job-search-card__listdate--new").Attr("datetime")
			}
			if publishedAt == "" {
				publishedAt = time.Now().Format("2006-01-02")
			}

			workMode, cleanLoc := detectWorkMode(location, "")
			location = cleanLoc

			details := fetchJobDetails(jobLink)
			time.Sleep(500 * time.Millisecond)

			if workMode == "onsite" {
				workMode, _ = detectWorkMode(location, details.Description)
			}

			jobType, _ := determineJobType(title, details.Description)
			if jobType == "" {
				jobType = "junior_position"
			}

			job := &JsonVacancy{
				CompanyName:       company,
				CompanyUrl:        companyUrl,
				CompanyProfileUrl: companyUrl,
				Title:             title,
				Region:            location,
				ApplicationUrl:    jobLink,
				Type:              jobType,
				Tags:              extractSkills(title, details.Description, allSkills),
				Description:       details.Description,
				WorkMode:          workMode,
				EmploymentType:    details.EmploymentType,
				PublishedAt:       publishedAt,
				LogoUrl:           logoUrl,
			}

			uniqueJobs[key] = job
			*orderedKeys = append(*orderedKeys, key)
			pageFound++
			added++
		})

		if pageFound == 0 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	return added
}

// scrapeLinkedIn runs multiple targeted LinkedIn search passes to maximise
// coverage of entry-level roles in Portugal:
//
//   Pass A — Estágios & Trainees (f_E=1, keywords: estágio/trainee)
//             → internships and curricular/extracurricular placements
//   Pass B — Entry-level sem keywords (f_E=2)
//             → qualquer vaga catalogada como entry-level pelo recrutador,
//               independentemente do título (apanha "Analista", "Consultor", etc.)
//   Pass C — Júniores explícitos (f_E=2,3, keywords: junior/recém-licenciado)
//             → posições com ≤3 anos de experiência exigida e título explícito
//
// Todos os passes partilham o mesmo seenJobs e uniqueJobs, logo não há duplicados.
func scrapeLinkedIn(allSkills []string, seenJobs map[string]bool) []JsonVacancy {
	uniqueJobs := make(map[string]*JsonVacancy)
	var orderedKeys []string
	client := &http.Client{Timeout: 30 * time.Second}

	// LinkedIn f_E values:
	//   1 = Internship (estágio)
	//   2 = Entry level (≤ 2 anos experiência)
	//   3 = Associate   (≤ 3 anos experiência)

	// ── Pass A: Estágios & Trainees ──────────────────────────────────────────
	passA := "https://www.linkedin.com/jobs/search?keywords=estagio%%20OR%%20trainee%%20OR%%20internship&location=Portugal&geoId=100364837&f_E=1&sortBy=R&start=%d"
	nA := linkedInSearchPass(client, passA, "PassA-estagios", allSkills, seenJobs, uniqueJobs, &orderedKeys)
	log.Printf("[jobs][linkedin] Pass A (estágios/trainees) → %d novos empregos", nA)

	time.Sleep(3 * time.Second) // pausa entre passes para evitar rate limit

	// ── Pass B: Entry-level sem keywords (apanha títulos que não dizem "júnior") ─
	passB := "https://www.linkedin.com/jobs/search?keywords=&location=Portugal&geoId=100364837&f_E=2&sortBy=R&start=%d"
	nB := linkedInSearchPass(client, passB, "PassB-entry-level", allSkills, seenJobs, uniqueJobs, &orderedKeys)
	log.Printf("[jobs][linkedin] Pass B (entry-level, sem keywords) → %d novos empregos", nB)

	time.Sleep(3 * time.Second)

	// ── Pass C: Júniores explícitos (f_E=2,3 + keywords de júnior) ───────────
	passC := "https://www.linkedin.com/jobs/search?keywords=junior%%20OR%%20j%%C3%%BAnior%%20OR%%20rec%%C3%%A9m-licenciado%%20OR%%20graduate&location=Portugal&geoId=100364837&f_E=2,3&sortBy=R&start=%d"
	nC := linkedInSearchPass(client, passC, "PassC-junior", allSkills, seenJobs, uniqueJobs, &orderedKeys)
	log.Printf("[jobs][linkedin] Pass C (júniores explícitos, ≤3 anos) → %d novos empregos", nC)

	var vacancies []JsonVacancy
	for _, k := range orderedKeys {
		vacancies = append(vacancies, *uniqueJobs[k])
	}
	return vacancies
}

func determineJobType(title, desc string) (string, bool) {
	textLower := strings.ToLower(title + " " + desc)

	if strings.Contains(textLower, "estágio de verão") || strings.Contains(textLower, "estagio de verao") || strings.Contains(textLower, "summer internship") {
		return "summer_internship", true
	}
	if strings.Contains(textLower, "estágio curricular") || strings.Contains(textLower, "estagio curricular") || strings.Contains(textLower, "curricular internship") {
		return "curricular_internship", true
	}
	if strings.Contains(textLower, "estágio extracurricular") || strings.Contains(textLower, "estagio extracurricular") || strings.Contains(textLower, "extracurricular internship") {
		return "extracurricular_internship", true
	}
	if strings.Contains(textLower, "estágio") || strings.Contains(textLower, "estagio") || strings.Contains(textLower, "internship") || strings.Contains(textLower, "trainee") {
		return "professional_internship", true
	}

	juniorKeywords := []string{
		"junior", "júnior", "recém-licenciado", "recem-licenciado", "entry level", "entry-level",
		"primeiro emprego", "recent graduate",
	}

	for _, kw := range juniorKeywords {
		if strings.Contains(textLower, kw) {
			return "junior_position", true
		}
	}
	return "", false
}

// saveVacanciesToJson merges new vacancies with the existing set already saved
// in GCS (or local file), prunes entries older than 45 days, caps at maxVacancies
// total, and persists the result. This means each scrape RUN accumulates results
// instead of replacing them, keeping the list full even when individual platforms
// return few results on a given day.
const maxVacancies = 400 // memory-safe cap for Cloud Run

func saveVacanciesToJson(newVacancies []JsonVacancy) {
	cutoff := time.Now().AddDate(0, 0, -45)

	// ── 1. Load existing data ─────────────────────────────────────────────
	var existing []JsonVacancy
	if storage.GCSClient != nil {
		if raw, err := storage.ReadObject(GCSVagasKey); err == nil {
			_ = json.Unmarshal(raw, &existing)
		}
	} else {
		path := filepath.Join("config", "vagas_final.json")
		if raw, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(raw, &existing)
		}
	}

	// ── 2. Build dedup map from existing (keeping only non-expired) ───────
	type key struct{ company, title string }
	unique := make(map[key]*JsonVacancy)
	var order []key

	for i := range existing {
		v := &existing[i]
		// Drop vacancies older than 45 days
		if v.PublishedAt != "" {
			if t, err := time.Parse("2006-01-02", v.PublishedAt); err == nil && t.Before(cutoff) {
				continue
			}
		}
		k := key{v.CompanyName, v.Title}
		if unique[k] == nil {
			unique[k] = v
			order = append(order, k)
		}
	}

	// ── 3. Merge new vacancies (newer entries take priority) ──────────────
	for i := range newVacancies {
		v := &newVacancies[i]
		k := key{v.CompanyName, v.Title}
		if unique[k] == nil {
			unique[k] = v
			order = append(order, k)
		} else {
			// Update with fresher data
			unique[k] = v
		}
	}

	// ── 4. Assemble & cap ─────────────────────────────────────────────────
	result := make([]JsonVacancy, 0, len(order))
	for _, k := range order {
		if v, ok := unique[k]; ok {
			result = append(result, *v)
		}
	}
	// Keep the most recent maxVacancies entries (tail of the slice = newest)
	if len(result) > maxVacancies {
		result = result[len(result)-maxVacancies:]
	}

	log.Printf("[jobs] Saving %d vacancies (%d existing + %d new, capped at %d)",
		len(result), len(existing), len(newVacancies), maxVacancies)

	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Printf("[jobs] Error encoding vacancies JSON: %v", err)
		return
	}

	// ── 5. Persist ────────────────────────────────────────────────────────
	if storage.GCSClient != nil {
		if err := storage.WriteObject(GCSVagasKey, data, "application/json"); err != nil {
			log.Printf("[jobs] Error writing vagas_final.json to GCS: %v", err)
		} else {
			log.Println("[jobs] vagas_final.json saved to GCS successfully")
			CacheVersion.Add(1)
			return
		}
	}

	path := filepath.Join("config", "vagas_final.json")
	if err := os.MkdirAll("config", 0755); err != nil {
		log.Printf("[jobs] Error creating config dir: %v", err)
		return
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		log.Printf("[jobs] Error saving vacancies JSON locally: %v", err)
	} else {
		log.Println("[jobs] vagas_final.json saved locally (GCS unavailable)")
		CacheVersion.Add(1)
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

type JobDetails struct {
	Description    string
	EmploymentType string
}

func fetchJobDetails(jobUrl string) JobDetails {
	details := JobDetails{
		Description:    "Para mais detalhes, visite o link da candidatura.",
		EmploymentType: "full_time",
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", jobUrl, nil)
	if err != nil {
		return details
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/115.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return details
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return details
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return details
	}

	// Extract Employment Type from Criteria
	doc.Find("li.description__job-criteria-item").Each(func(i int, s *goquery.Selection) {
		header := strings.TrimSpace(strings.ToLower(s.Find("h3.description__job-criteria-subheader").Text()))
		value := strings.TrimSpace(s.Find("span.description__job-criteria-text").Text())
		if strings.Contains(header, "employment type") || strings.Contains(header, "tipo de emprego") {
			if value != "" {
				valueLower := strings.ToLower(value)
				if strings.Contains(valueLower, "full-time") || strings.Contains(valueLower, "tempo integral") || strings.Contains(valueLower, "estágio") {
					details.EmploymentType = "full_time"
				} else if strings.Contains(valueLower, "part-time") || strings.Contains(valueLower, "meio período") || strings.Contains(valueLower, "voluntário") {
					details.EmploymentType = "part_time"
				} else if strings.Contains(valueLower, "contract") || strings.Contains(valueLower, "contrato") {
					details.EmploymentType = "contract"
				} else {
					details.EmploymentType = "full_time"
				}
			}
		}
	})

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
			details.Description = text
		}
	}

	return details
}
