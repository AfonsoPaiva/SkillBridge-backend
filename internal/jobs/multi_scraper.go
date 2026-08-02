package jobs

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// ─────────────────────────────────────────────────────────────────────────────
// Shared helpers
// ─────────────────────────────────────────────────────────────────────────────

func newBrowserClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

func doGet(client *http.Client, rawURL string) (*goquery.Document, int, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "pt-PT,pt;q=0.9,en-US;q=0.8,en;q=0.7")

	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, resp.StatusCode, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	return doc, resp.StatusCode, err
}

// cleanLocation strips work-mode suffixes in parentheses from location strings.
var workModeParenRE = regexp.MustCompile(`(?i)\s*\((hybrid|remote|on-site|híbrido|remoto|presencial)\)`)

func detectWorkMode(location, description string) (workMode, cleanLoc string) {
	locLower := strings.ToLower(location)
	workMode = "onsite"
	if strings.Contains(locLower, "hybrid") || strings.Contains(locLower, "híbrido") {
		workMode = "hybrid"
	} else if strings.Contains(locLower, "remote") || strings.Contains(locLower, "remoto") {
		workMode = "remote"
	}
	cleanLoc = workModeParenRE.ReplaceAllString(location, "")

	if workMode == "onsite" {
		descLower := strings.ToLower(description)
		if strings.Contains(descLower, "hybrid") || strings.Contains(descLower, "híbrido") {
			workMode = "hybrid"
		} else if strings.Contains(descLower, "remote") || strings.Contains(descLower, "remoto") {
			workMode = "remote"
		}
	}
	return
}

// faviconURL returns a Google favicon URL for the given company website or name.
func faviconURL(companyURL, companyName string) string {
	domain := companyURL
	if domain == "" {
		domain = strings.ToLower(strings.ReplaceAll(companyName, " ", "")) + ".com"
	} else {
		domain = strings.TrimPrefix(domain, "http://")
		domain = strings.TrimPrefix(domain, "https://")
		domain = strings.TrimPrefix(domain, "www.")
		if idx := strings.Index(domain, "/"); idx != -1 {
			domain = domain[:idx]
		}
	}
	return "https://t1.gstatic.com/faviconV2?client=SOCIAL&type=FAVICON&fallback_opts=TYPE,SIZE,URL&url=http://" + domain + "&size=128"
}

// ─────────────────────────────────────────────────────────────────────────────
// Indeed Portugal scraper
// ─────────────────────────────────────────────────────────────────────────────

func ScrapeIndeedJobs(allSkills []string, seen map[string]bool) []JsonVacancy {
	log.Println("[jobs] Starting Indeed scraper (multi-pass: estágio + trainee + júnior + recém-licenciado)...")

	client := newBrowserClient(30 * time.Second)
	uniqueJobs := make(map[string]*JsonVacancy)
	var orderedKeys []string

	// indeedPass executes one paginated Indeed search and merges results.
	// We trust the search query — any job returned by Indeed for "estágio" IS an
	// estágio, so we do NOT re-filter by title keywords (that was discarding valid jobs).
	// We still run determineJobType to correctly classify the Type field.
	indeedPass := func(query, passLabel string, maxPages int) int {
		added := 0
		for page := 0; page < maxPages; page++ {
			start := page * 10
			searchURL := fmt.Sprintf(
				"https://pt.indeed.com/jobs?q=%s&l=Portugal&sort=date&start=%d",
				url.QueryEscape(query), start,
			)

			doc, _, err := doGet(client, searchURL)
			if err != nil {
				log.Printf("[jobs][indeed][%s] Error fetching page %d: %v", passLabel, page, err)
				break
			}

			pageFound := 0
			doc.Find("div.job_seen_beacon, div.resultWithShelf").Each(func(_ int, s *goquery.Selection) {
				title := strings.TrimSpace(s.Find("h2.jobTitle span[title], h2.jobTitle a span").First().Text())
				if title == "" {
					title = strings.TrimSpace(s.Find("h2.jobTitle").Text())
				}
				company := strings.TrimSpace(s.Find("span[data-testid='company-name'], .companyName").First().Text())
				location := strings.TrimSpace(s.Find("div[data-testid='text-location'], .companyLocation").First().Text())

				// Build the canonical job URL
				jk, _ := s.Find("a[data-jk], a[id^='job_']").First().Attr("data-jk")
				if jk == "" {
					href, _ := s.Find("h2.jobTitle a, a.jcs-JobTitle").First().Attr("href")
					if href == "" {
						return
					}
					if strings.HasPrefix(href, "/") {
						href = "https://pt.indeed.com" + href
					}
					if idx := strings.Index(href, "?"); idx != -1 {
						href = href[:idx]
					}
					jk = href
				} else {
					jk = "https://pt.indeed.com/viewjob?jk=" + jk
				}

				if title == "" || company == "" {
					return
				}

				// Global dedup across all passes (by URL and by company+title)
				key := company + "|" + title
				if seen[jk] || uniqueJobs[key] != nil {
					return
				}
				seen[jk] = true

				// Classify type using title first; trust query result — don't discard
				jobType, _ := determineJobType(title, "")
				if jobType == "" {
					jobType = "junior_position" // sensible default; refined after fetching description
				}

				workMode, cleanLoc := detectWorkMode(location, "")
				publishedAt := time.Now().Format("2006-01-02")

				details := fetchJobDetails(jk)
				time.Sleep(500 * time.Millisecond)

				if workMode == "onsite" {
					workMode, _ = detectWorkMode(cleanLoc, details.Description)
				}
				// Refine type with full description
				if jt, ok := determineJobType(title, details.Description); ok {
					jobType = jt
				}

				job := &JsonVacancy{
					CompanyName:       company,
					CompanyUrl:        "",
					CompanyProfileUrl: "",
					Title:             title,
					Region:            strings.TrimSpace(cleanLoc),
					ApplicationUrl:    jk,
					Type:              jobType,
					Tags:              extractSkills(title, details.Description, allSkills),
					Description:       details.Description,
					WorkMode:          workMode,
					EmploymentType:    details.EmploymentType,
					PublishedAt:       publishedAt,
					LogoUrl:           faviconURL("", company),
				}
				uniqueJobs[key] = job
				orderedKeys = append(orderedKeys, key)
				pageFound++
				added++
			})

			if pageFound == 0 {
				break
			}
			time.Sleep(2 * time.Second)
		}
		log.Printf("[jobs][indeed][%s] → %d novos empregos", passLabel, added)
		time.Sleep(3 * time.Second) // pausa entre passes
		return added
	}

	// ── Pass A: Estágios ──────────────────────────────────────────────────────
	// URL: https://pt.indeed.com/jobs?q=est%C3%A1gio&l=Portugal&sort=date
	indeedPass("estágio", "estagios", 8)

	// ── Pass B: Trainee ───────────────────────────────────────────────────────
	indeedPass("trainee", "trainee", 6)

	// ── Pass C: Júnior ────────────────────────────────────────────────────────
	indeedPass("júnior", "junior", 8)

	// ── Pass D: Recém-Licenciado ──────────────────────────────────────────────
	indeedPass("recém-licenciado", "recem-licenciado", 6)

	// ── Pass E: Graduate (apanha inglês) ─────────────────────────────────────
	indeedPass("graduate", "graduate", 4)

	var vacancies []JsonVacancy
	for _, k := range orderedKeys {
		vacancies = append(vacancies, *uniqueJobs[k])
	}
	log.Printf("[jobs][indeed] Total unique jobs: %d", len(vacancies))
	return vacancies
}

// ─────────────────────────────────────────────────────────────────────────────
// Net-Empregos scraper  (maior portal de emprego em Portugal)
// URL correcta: https://www.net-empregos.com/pesquisa-empregos.asp?chaves=<query>&cidade=&categoria=0&zona=0&tipo=0&pagina=<n>
// ─────────────────────────────────────────────────────────────────────────────

func ScrapeNetEmpregos(allSkills []string, seen map[string]bool) []JsonVacancy {
	log.Println("[jobs] Starting Net-Empregos scraper (multi-pass: estágio + trainee + júnior + recém-licenciado)...")

	client := newBrowserClient(30 * time.Second)
	uniqueJobs := make(map[string]*JsonVacancy)
	var orderedKeys []string

	// netPass executes one paginated Net-Empregos search and merges results.
	// We trust the search query — no secondary title filter applied.
	netPass := func(query, passLabel string, maxPages int) int {
		added := 0
		for page := 1; page <= maxPages; page++ {
			// Correct URL: parameter is 'chaves' (not 'q'), with required extra params
			searchURL := fmt.Sprintf(
				"https://www.net-empregos.com/pesquisa-empregos.asp?chaves=%s&cidade=&categoria=0&zona=0&tipo=0&pagina=%d",
				url.QueryEscape(query), page,
			)

			doc, status, err := doGet(client, searchURL)
			if err != nil {
				log.Printf("[jobs][netempregos][%s] Error fetching page %d: %v (status=%d)", passLabel, page, err, status)
				break
			}

			pageFound := 0
			doc.Find("div.oferta, div.job-result, article.job-item, div[class*='oferta']").Each(func(_ int, s *goquery.Selection) {
				title := strings.TrimSpace(s.Find("h2 a, h3 a, .titulo a, a.titulo-oferta, span.titulo").First().Text())
				if title == "" {
					title = strings.TrimSpace(s.Find("a[href*='/emprego/']").First().Text())
				}

				company := strings.TrimSpace(s.Find(".empresa, .company, span.empresa-nome, a.empresa-link").First().Text())
				location := strings.TrimSpace(s.Find(".localidade, .local, span.local-oferta, .zona").First().Text())

				jobLink, exists := s.Find("h2 a, h3 a, a[href*='/emprego/'], a.titulo-oferta").First().Attr("href")
				if !exists || jobLink == "" {
					return
				}
				if strings.HasPrefix(jobLink, "/") {
					jobLink = "https://www.net-empregos.com" + jobLink
				}
				cleanLink := jobLink
				if idx := strings.Index(cleanLink, "?"); idx != -1 {
					cleanLink = cleanLink[:idx]
				}

				if title == "" || company == "" {
					return
				}

				// Global dedup across all passes
				key := company + "|" + title
				if seen[cleanLink] || uniqueJobs[key] != nil {
					return
				}
				seen[cleanLink] = true

				// Classify type; trust search query — don't discard based on title
				jobType, _ := determineJobType(title, "")
				if jobType == "" {
					jobType = "junior_position"
				}

				// Logo: try image inside card, fallback to favicon
				logoURL, _ := s.Find("img.logo-empresa, img.company-logo, img[alt*='logo']").First().Attr("src")
				if logoURL == "" {
					logoURL = faviconURL("", company)
				} else if strings.HasPrefix(logoURL, "/") {
					logoURL = "https://www.net-empregos.com" + logoURL
				}

				workMode, cleanLoc := detectWorkMode(location, "")
				publishedAt := time.Now().Format("2006-01-02")

				dateAttr := s.Find("time").First().AttrOr("datetime", "")
				if dateAttr != "" {
					publishedAt = dateAttr
				}

				details := fetchJobDetails(jobLink)
				time.Sleep(500 * time.Millisecond)

				if workMode == "onsite" {
					workMode, _ = detectWorkMode(cleanLoc, details.Description)
				}
				if jt, ok := determineJobType(title, details.Description); ok {
					jobType = jt
				}

				job := &JsonVacancy{
					CompanyName:       company,
					CompanyUrl:        "",
					CompanyProfileUrl: "",
					Title:             title,
					Region:            strings.TrimSpace(cleanLoc),
					ApplicationUrl:    jobLink,
					Type:              jobType,
					Tags:              extractSkills(title, details.Description, allSkills),
					Description:       details.Description,
					WorkMode:          workMode,
					EmploymentType:    details.EmploymentType,
					PublishedAt:       publishedAt,
					LogoUrl:           logoURL,
				}
				uniqueJobs[key] = job
				orderedKeys = append(orderedKeys, key)
				pageFound++
				added++
			})

			if pageFound == 0 {
				break
			}
			time.Sleep(2 * time.Second)
		}
		log.Printf("[jobs][netempregos][%s] → %d novos empregos", passLabel, added)
		time.Sleep(3 * time.Second)
		return added
	}

	// ── Pass A: Estágio ───────────────────────────────────────────────────────
	netPass("estágio", "estagio", 8)

	// ── Pass B: Estagiário (variante PT) ─────────────────────────────────────
	netPass("estagiário", "estagiario", 6)

	// ── Pass C: Trainee ───────────────────────────────────────────────────────
	netPass("trainee", "trainee", 6)

	// ── Pass D: Júnior ────────────────────────────────────────────────────────
	netPass("júnior", "junior", 8)

	// ── Pass E: Recém-Licenciado ──────────────────────────────────────────────
	netPass("recém-licenciado", "recem-licenciado", 6)

	var vacancies []JsonVacancy
	for _, k := range orderedKeys {
		vacancies = append(vacancies, *uniqueJobs[k])
	}
	log.Printf("[jobs][netempregos] Total unique jobs: %d", len(vacancies))
	return vacancies
}



// ─────────────────────────────────────────────────────────────────────────────
// Expresso Emprego scraper
// URL: https://expressoemprego.pt/pesquisa?q=<query>&page=<n>
// ─────────────────────────────────────────────────────────────────────────────

func ScrapeExpressoEmprego(allSkills []string, seen map[string]bool) []JsonVacancy {
	log.Println("[jobs] Starting Expresso Emprego scraper...")

	client := newBrowserClient(30 * time.Second)
	var vacancies []JsonVacancy
	uniqueJobs := make(map[string]*JsonVacancy)
	var orderedKeys []string

	queries := []string{"estágio", "junior", "trainee"}

	for _, q := range queries {
		for page := 1; page <= 5; page++ {
			searchURL := fmt.Sprintf(
				"https://expressoemprego.pt/pesquisa?q=%s&page=%d",
				url.QueryEscape(q), page,
			)

			doc, status, err := doGet(client, searchURL)
			if err != nil {
				log.Printf("[jobs][expresso] Error fetching page %d for '%s': %v (status=%d)", page, q, err, status)
				break
			}

			jobsFound := 0

			// Expresso Emprego: each job card is an <article> or a div with class "job-offer" / "offer-box"
			doc.Find("article.job-offer, div.offer-box, li.job-listing, div.job-card").Each(func(_ int, s *goquery.Selection) {
				title := strings.TrimSpace(s.Find("h2 a, h3 a, .job-title a, .offer-title a").First().Text())
				company := strings.TrimSpace(s.Find(".company-name, .empresa, .employer-name").First().Text())
				location := strings.TrimSpace(s.Find(".location, .localidade, .job-location").First().Text())

				jobLink, exists := s.Find("h2 a, h3 a, .job-title a, a.job-link").First().Attr("href")
				if !exists || jobLink == "" {
					return
				}
				if strings.HasPrefix(jobLink, "/") {
					jobLink = "https://expressoemprego.pt" + jobLink
				}
				cleanLink := jobLink
				if idx := strings.Index(cleanLink, "?"); idx != -1 {
					cleanLink = cleanLink[:idx]
				}

				if title == "" || company == "" {
					return
				}
				key := company + "|" + title
				if seen[cleanLink] || uniqueJobs[key] != nil {
					return
				}
				seen[cleanLink] = true

				jobType, isJunior := determineJobType(title, "")
				if !isJunior {
					isJunior = isLikelyJuniorTitle(title)
					if !isJunior {
						return
					}
					jobType = "junior_position"
				}

				logoURL, _ := s.Find("img.company-logo, img.logo, img[alt*='logo']").First().Attr("src")
				if logoURL == "" {
					logoURL = faviconURL("", company)
				} else if strings.HasPrefix(logoURL, "/") {
					logoURL = "https://expressoemprego.pt" + logoURL
				}

				workMode, cleanLoc := detectWorkMode(location, "")
				publishedAt := time.Now().Format("2006-01-02")

				dateAttr := s.Find("time").First().AttrOr("datetime", "")
				if dateAttr != "" {
					publishedAt = dateAttr
				}

				details := fetchJobDetails(jobLink)
				time.Sleep(500 * time.Millisecond)

				if workMode == "onsite" {
					workMode, _ = detectWorkMode(cleanLoc, details.Description)
				}
				if jt, ok := determineJobType(title, details.Description); ok {
					jobType = jt
				}

				job := &JsonVacancy{
					CompanyName:       company,
					CompanyUrl:        "",
					CompanyProfileUrl: "",
					Title:             title,
					Region:            strings.TrimSpace(cleanLoc),
					ApplicationUrl:    jobLink,
					Type:              jobType,
					Tags:              extractSkills(title, details.Description, allSkills),
					Description:       details.Description,
					WorkMode:          workMode,
					EmploymentType:    details.EmploymentType,
					PublishedAt:       publishedAt,
					LogoUrl:           logoURL,
				}
				uniqueJobs[key] = job
				orderedKeys = append(orderedKeys, key)
				jobsFound++
			})

			if jobsFound == 0 {
				break
			}
			time.Sleep(2 * time.Second)
		}
		time.Sleep(3 * time.Second)
	}

	for _, k := range orderedKeys {
		vacancies = append(vacancies, *uniqueJobs[k])
	}
	log.Printf("[jobs][expresso] Scraped %d jobs", len(vacancies))
	return vacancies
}

// ─────────────────────────────────────────────────────────────────────────────
// Emprego.pt scraper
// ─────────────────────────────────────────────────────────────────────────────

func ScrapeEmpregoJobs(allSkills []string, seen map[string]bool) []JsonVacancy {
	log.Println("[jobs] Starting Emprego.pt scraper...")

	client := newBrowserClient(30 * time.Second)
	var vacancies []JsonVacancy
	uniqueJobs := make(map[string]*JsonVacancy)
	var orderedKeys []string

	queries := []string{"estágio", "junior", "trainee"}

	for _, q := range queries {
		for page := 1; page <= 5; page++ {
			searchURL := fmt.Sprintf(
				"https://www.emprego.pt/ofertas-emprego/?q=%s&l=Portugal&p=%d",
				url.QueryEscape(q), page,
			)

			doc, _, err := doGet(client, searchURL)
			if err != nil {
				log.Printf("[jobs][emprego] Error fetching page %d for query '%s': %v", page, q, err)
				break
			}

			jobsFound := 0

			doc.Find("article.offer-item, div.offer-card, li.oferta").Each(func(_ int, s *goquery.Selection) {
				title := strings.TrimSpace(s.Find("h2.title a, h2 a, .offer-title a, .oferta-titulo a").First().Text())
				company := strings.TrimSpace(s.Find(".company-name, .empresa, .offer-company").First().Text())
				location := strings.TrimSpace(s.Find(".location, .localidade, .offer-location").First().Text())
				jobLink, exists := s.Find("h2.title a, .offer-title a, h2 a").First().Attr("href")

				if !exists || jobLink == "" {
					return
				}
				if strings.HasPrefix(jobLink, "/") {
					jobLink = "https://www.emprego.pt" + jobLink
				}
				cleanLink := jobLink
				if idx := strings.Index(cleanLink, "?"); idx != -1 {
					cleanLink = cleanLink[:idx]
				}

				if title == "" || company == "" {
					return
				}
				key := company + "|" + title
				if seen[cleanLink] || uniqueJobs[key] != nil {
					return
				}
				seen[cleanLink] = true

				jobType, isJunior := determineJobType(title, "")
				if !isJunior {
					isJunior = isLikelyJuniorTitle(title)
					if !isJunior {
						return
					}
					jobType = "junior_position"
				}

				logoURL, _ := s.Find("img.company-logo, img.logo").First().Attr("src")
				if logoURL == "" {
					logoURL = faviconURL("", company)
				} else if strings.HasPrefix(logoURL, "/") {
					logoURL = "https://www.emprego.pt" + logoURL
				}

				workMode, cleanLoc := detectWorkMode(location, "")
				publishedAt := time.Now().Format("2006-01-02")

				dateText := strings.TrimSpace(s.Find("time, .date, .data").First().AttrOr("datetime", ""))
				if dateText != "" {
					publishedAt = dateText
				}

				details := fetchJobDetails(jobLink)
				time.Sleep(500 * time.Millisecond)

				if workMode == "onsite" {
					workMode, _ = detectWorkMode(cleanLoc, details.Description)
				}
				if jt, ok := determineJobType(title, details.Description); ok {
					jobType = jt
				}

				job := &JsonVacancy{
					CompanyName:       company,
					CompanyUrl:        "",
					CompanyProfileUrl: "",
					Title:             title,
					Region:            strings.TrimSpace(cleanLoc),
					ApplicationUrl:    jobLink,
					Type:              jobType,
					Tags:              extractSkills(title, details.Description, allSkills),
					Description:       details.Description,
					WorkMode:          workMode,
					EmploymentType:    details.EmploymentType,
					PublishedAt:       publishedAt,
					LogoUrl:           logoURL,
				}
				uniqueJobs[key] = job
				orderedKeys = append(orderedKeys, key)
				jobsFound++
			})

			if jobsFound == 0 {
				break
			}
			time.Sleep(2 * time.Second)
		}
		time.Sleep(3 * time.Second)
	}

	for _, k := range orderedKeys {
		vacancies = append(vacancies, *uniqueJobs[k])
	}
	log.Printf("[jobs][emprego] Scraped %d jobs", len(vacancies))
	return vacancies
}

// ─────────────────────────────────────────────────────────────────────────────
// Jobsora Portugal scraper
// URL: https://pt.jobsora.com/empregos?experience=0&p=<n>
// ─────────────────────────────────────────────────────────────────────────────

func ScrapeJobsoraJobs(allSkills []string, seen map[string]bool) []JsonVacancy {
	log.Println("[jobs] Starting Jobsora scraper (experience=0,1,2,3 & estagios)...")

	client := newBrowserClient(30 * time.Second)
	uniqueJobs := make(map[string]*JsonVacancy)
	var orderedKeys []string

	jobsoraPass := func(expVal, query, passLabel string, maxPages int) int {
		added := 0
		for page := 1; page <= maxPages; page++ {
			searchURL := fmt.Sprintf(
				"https://pt.jobsora.com/empregos?experience=%s&q=%s&p=%d",
				expVal, url.QueryEscape(query), page,
			)

			doc, status, err := doGet(client, searchURL)
			if err != nil {
				log.Printf("[jobs][jobsora][%s] Error fetching page %d: %v (status=%d)", passLabel, page, err, status)
				break
			}

			pageFound := 0
			doc.Find("div.c-job-item, article.c-job-item, div.job-card, div.c-job-card, article[class*='job']").Each(func(_ int, s *goquery.Selection) {
				title := strings.TrimSpace(s.Find("a.c-job-item__title, .c-job-item__title a, h2 a, h3 a").First().Text())
				company := strings.TrimSpace(s.Find("span.c-job-item__company, .c-job-item__company, div.c-job-item__company, .company-name").First().Text())
				location := strings.TrimSpace(s.Find("span.c-job-item__location, .c-job-item__location, .location").First().Text())

				jobLink, exists := s.Find("a.c-job-item__title, .c-job-item__title a, h2 a, a[href*='/emprego']").First().Attr("href")
				if !exists || jobLink == "" {
					return
				}
				if strings.HasPrefix(jobLink, "/") {
					jobLink = "https://pt.jobsora.com" + jobLink
				}
				cleanLink := jobLink
				if idx := strings.Index(cleanLink, "?"); idx != -1 {
					cleanLink = cleanLink[:idx]
				}

				if title == "" || company == "" {
					return
				}

				key := company + "|" + title
				if seen[cleanLink] || uniqueJobs[key] != nil {
					return
				}
				seen[cleanLink] = true

				jobType, _ := determineJobType(title, "")
				if jobType == "" {
					jobType = "junior_position"
				}

				workMode, cleanLoc := detectWorkMode(location, "")
				publishedAt := time.Now().Format("2006-01-02")

				details := fetchJobDetails(jobLink)
				time.Sleep(500 * time.Millisecond)

				if workMode == "onsite" {
					workMode, _ = detectWorkMode(cleanLoc, details.Description)
				}
				if jt, ok := determineJobType(title, details.Description); ok {
					jobType = jt
				}

				job := &JsonVacancy{
					CompanyName:       company,
					CompanyUrl:        "",
					CompanyProfileUrl: "",
					Title:             title,
					Region:            strings.TrimSpace(cleanLoc),
					ApplicationUrl:    jobLink,
					Type:              jobType,
					Tags:              extractSkills(title, details.Description, allSkills),
					Description:       details.Description,
					WorkMode:          workMode,
					EmploymentType:    details.EmploymentType,
					PublishedAt:       publishedAt,
					LogoUrl:           faviconURL("", company),
				}
				uniqueJobs[key] = job
				orderedKeys = append(orderedKeys, key)
				pageFound++
				added++
			})

			if pageFound == 0 {
				break
			}
			time.Sleep(2 * time.Second)
		}
		log.Printf("[jobs][jobsora][%s] → %d novos empregos", passLabel, added)
		time.Sleep(3 * time.Second)
		return added
	}

	// Experience level 0 (Sem experiência / Estágio)
	jobsoraPass("0", "", "exp0-sem-experiencia", 5)
	// Experience level 1 (1 ano)
	jobsoraPass("1", "", "exp1-um-ano", 5)
	// Experience level 2 (2 anos)
	jobsoraPass("2", "junior", "exp2-dois-anos", 4)
	// Targeted search for estagios
	jobsoraPass("", "estágio", "estagio-kw", 4)

	var vacancies []JsonVacancy
	for _, k := range orderedKeys {
		vacancies = append(vacancies, *uniqueJobs[k])
	}
	log.Printf("[jobs][jobsora] Total unique jobs: %d", len(vacancies))
	return vacancies
}

// ─────────────────────────────────────────────────────────────────────────────
// Jooble Portugal scraper
// URL: https://pt.jooble.org/SearchResult?workExp=2&p=<n>
// ─────────────────────────────────────────────────────────────────────────────

func ScrapeJoobleJobs(allSkills []string, seen map[string]bool) []JsonVacancy {
	log.Println("[jobs] Starting Jooble scraper (workExp=1,2 & junior/estágio)...")

	client := newBrowserClient(30 * time.Second)
	uniqueJobs := make(map[string]*JsonVacancy)
	var orderedKeys []string

	jooblePass := func(workExp, query, passLabel string, maxPages int) int {
		added := 0
		for page := 1; page <= maxPages; page++ {
			searchURL := fmt.Sprintf(
				"https://pt.jooble.org/SearchResult?rgns=Portugal&q=%s&workExp=%s&p=%d",
				url.QueryEscape(query), workExp, page,
			)

			doc, status, err := doGet(client, searchURL)
			if err != nil {
				log.Printf("[jobs][jooble][%s] Error fetching page %d: %v (status=%d)", passLabel, page, err, status)
				break
			}

			pageFound := 0
			doc.Find("article[data-test-name='_jobCard'], div[data-test-name='_jobCard'], article.card-job, div.vacancy-card, article[class*='job']").Each(func(_ int, s *goquery.Selection) {
				title := strings.TrimSpace(s.Find("h2 a, a[data-test-name='_jobTitle'], .job-title a, h2").First().Text())
				company := strings.TrimSpace(s.Find("span[data-test-name='_companyName'], .company-name, span.company, p[class*='company']").First().Text())
				location := strings.TrimSpace(s.Find("span[data-test-name='_location'], .location-name, span.location, div[class*='location']").First().Text())

				jobLink, exists := s.Find("h2 a, a[data-test-name='_jobTitle'], a[href*='/desc/'], a[href*='jooble']").First().Attr("href")
				if !exists || jobLink == "" {
					return
				}
				if strings.HasPrefix(jobLink, "/") {
					jobLink = "https://pt.jooble.org" + jobLink
				}
				cleanLink := jobLink
				if idx := strings.Index(cleanLink, "?"); idx != -1 {
					cleanLink = cleanLink[:idx]
				}

				if title == "" || company == "" {
					return
				}

				key := company + "|" + title
				if seen[cleanLink] || uniqueJobs[key] != nil {
					return
				}
				seen[cleanLink] = true

				jobType, _ := determineJobType(title, "")
				if jobType == "" {
					jobType = "junior_position"
				}

				workMode, cleanLoc := detectWorkMode(location, "")
				publishedAt := time.Now().Format("2006-01-02")

				details := fetchJobDetails(jobLink)
				time.Sleep(500 * time.Millisecond)

				if workMode == "onsite" {
					workMode, _ = detectWorkMode(cleanLoc, details.Description)
				}
				if jt, ok := determineJobType(title, details.Description); ok {
					jobType = jt
				}

				job := &JsonVacancy{
					CompanyName:       company,
					CompanyUrl:        "",
					CompanyProfileUrl: "",
					Title:             title,
					Region:            strings.TrimSpace(cleanLoc),
					ApplicationUrl:    jobLink,
					Type:              jobType,
					Tags:              extractSkills(title, details.Description, allSkills),
					Description:       details.Description,
					WorkMode:          workMode,
					EmploymentType:    details.EmploymentType,
					PublishedAt:       publishedAt,
					LogoUrl:           faviconURL("", company),
				}
				uniqueJobs[key] = job
				orderedKeys = append(orderedKeys, key)
				pageFound++
				added++
			})

			if pageFound == 0 {
				break
			}
			time.Sleep(2 * time.Second)
		}
		log.Printf("[jobs][jooble][%s] → %d novos empregos", passLabel, added)
		time.Sleep(3 * time.Second)
		return added
	}

	// Pass 1: workExp=1 (Sem experiência / Pouca experiência)
	jooblePass("1", "estágio", "exp1-estagio", 5)
	// Pass 2: workExp=2 (Até 3 anos experiência)
	jooblePass("2", "junior", "exp2-junior", 5)
	// Pass 3: Trainee
	jooblePass("1", "trainee", "exp1-trainee", 4)

	var vacancies []JsonVacancy
	for _, k := range orderedKeys {
		vacancies = append(vacancies, *uniqueJobs[k])
	}
	log.Printf("[jobs][jooble] Total unique jobs: %d", len(vacancies))
	return vacancies
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// isLikelyJuniorTitle is a broadened pre-check for platforms where we don't
// have the full description yet. Intentionally more permissive than determineJobType.
func isLikelyJuniorTitle(title string) bool {
	t := strings.ToLower(title)
	keywords := []string{
		"estágio", "estagio", "estagiário", "estagiaria",
		"internship", "trainee", "junior", "júnior",
		"entry level", "entry-level",
		"recém-licenciado", "recem-licenciado",
		"graduate", "recente",
	}
	for _, kw := range keywords {
		if strings.Contains(t, kw) {
			return true
		}
	}
	return false
}

