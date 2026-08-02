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

// newBrowserClient returns an *http.Client with a realistic browser User-Agent.
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

// cleanLocation strips work-mode suffixes from location strings.
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

// ScrapeIndeedJobs scrapes junior/internship jobs from Indeed Portugal.
// The results share the same JsonVacancy schema.
func ScrapeIndeedJobs(allSkills []string, seen map[string]bool) []JsonVacancy {
	log.Println("[jobs] Starting Indeed scraper...")

	client := newBrowserClient(30 * time.Second)
	var vacancies []JsonVacancy
	uniqueJobs := make(map[string]*JsonVacancy)
	var orderedKeys []string

	queries := []string{
		"estágio",
		"junior",
		"trainee",
	}

	for _, q := range queries {
		for page := 0; page < 4; page++ { // 4 pages × ~15 results = ~60 per query
			start := page * 10
			searchURL := fmt.Sprintf(
				"https://pt.indeed.com/jobs?q=%s&l=Portugal&sort=date&start=%d",
				url.QueryEscape(q), start,
			)

			doc, _, err := doGet(client, searchURL)
			if err != nil {
				log.Printf("[jobs][indeed] Error fetching page %d for query '%s': %v", page, q, err)
				break
			}

			jobsFound := 0

			// Indeed uses div[data-jk] or td.resultContent cards
			doc.Find("div.job_seen_beacon, div.resultWithShelf").Each(func(_ int, s *goquery.Selection) {
				title := strings.TrimSpace(s.Find("h2.jobTitle span[title], h2.jobTitle a span").First().Text())
				if title == "" {
					title = strings.TrimSpace(s.Find("h2.jobTitle").Text())
				}

				company := strings.TrimSpace(s.Find("span[data-testid='company-name'], .companyName").First().Text())
				location := strings.TrimSpace(s.Find("div[data-testid='text-location'], .companyLocation").First().Text())

				// Build the application URL
				jk, _ := s.Find("a[data-jk], a[id^='job_']").First().Attr("data-jk")
				if jk == "" {
					href, _ := s.Find("h2.jobTitle a, a.jcs-JobTitle").First().Attr("href")
					if href == "" {
						return
					}
					if strings.HasPrefix(href, "/") {
						href = "https://pt.indeed.com" + href
					}
					// Strip tracking params
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

				key := company + "|" + title
				if seen[jk] || uniqueJobs[key] != nil {
					return
				}
				seen[jk] = true

				// Fetch company logo via favicon
				logoURL := faviconURL("", company)

				workMode, cleanLoc := detectWorkMode(location, "")
				publishedAt := time.Now().Format("2006-01-02")

				jobType, isJunior := determineJobType(title, "")
				if !isJunior {
					// Try with description later — use a lenient pre-check on title
					isJunior = isLikelyJuniorTitle(title)
					if !isJunior {
						return
					}
					// Re-determine type
					jobType, _ = determineJobType(title, "")
					if jobType == "" {
						jobType = "junior_position"
					}
				}

				// Fetch detailed description from the job page
				details := fetchJobDetails(jk)
				time.Sleep(500 * time.Millisecond)

				// Re-detect work mode with description
				if workMode == "onsite" {
					workMode, _ = detectWorkMode(cleanLoc, details.Description)
				}

				// Re-validate with full description
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

	log.Printf("[jobs][indeed] Scraped %d jobs", len(vacancies))
	return vacancies
}

// ─────────────────────────────────────────────────────────────────────────────
// Emprego.pt scraper
// ─────────────────────────────────────────────────────────────────────────────

// ScrapeEmpregoJobs scrapes junior/internship jobs from Emprego.pt.
func ScrapeEmpregoJobs(allSkills []string, seen map[string]bool) []JsonVacancy {
	log.Println("[jobs] Starting Emprego.pt scraper...")

	client := newBrowserClient(30 * time.Second)
	var vacancies []JsonVacancy
	uniqueJobs := make(map[string]*JsonVacancy)
	var orderedKeys []string

	queries := []string{
		"estágio",
		"junior",
		"trainee",
	}

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
				// Strip query params for dedup
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

				// Try to get logo from within the card
				logoURL, _ := s.Find("img.company-logo, img.logo").First().Attr("src")
				if logoURL == "" {
					logoURL = faviconURL("", company)
				} else if strings.HasPrefix(logoURL, "/") {
					logoURL = "https://www.emprego.pt" + logoURL
				}

				workMode, cleanLoc := detectWorkMode(location, "")
				publishedAt := time.Now().Format("2006-01-02")

				// Published date from card
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
// SAPO Emprego scraper
// ─────────────────────────────────────────────────────────────────────────────

// ScrapeSapoJobs scrapes junior/internship jobs from SAPO Emprego.
func ScrapeSapoJobs(allSkills []string, seen map[string]bool) []JsonVacancy {
	log.Println("[jobs] Starting SAPO Emprego scraper...")

	client := newBrowserClient(30 * time.Second)
	var vacancies []JsonVacancy
	uniqueJobs := make(map[string]*JsonVacancy)
	var orderedKeys []string

	queries := []string{
		"estágio",
		"junior",
		"trainee",
	}

	for _, q := range queries {
		for page := 1; page <= 5; page++ {
			searchURL := fmt.Sprintf(
				"https://emprego.sapo.pt/ofertas-emprego/?q=%s&l=portugal&pg=%d",
				url.QueryEscape(q), page,
			)

			doc, _, err := doGet(client, searchURL)
			if err != nil {
				log.Printf("[jobs][sapo] Error fetching page %d for query '%s': %v", page, q, err)
				break
			}

			jobsFound := 0

			doc.Find("div.offer, article.job-offer, li.job-listing").Each(func(_ int, s *goquery.Selection) {
				title := strings.TrimSpace(s.Find("h2 a, .job-title a, .offer-title").First().Text())
				company := strings.TrimSpace(s.Find(".company, .empresa, .job-company").First().Text())
				location := strings.TrimSpace(s.Find(".location, .localidade, .job-location").First().Text())
				jobLink, exists := s.Find("h2 a, .job-title a, a.job-link").First().Attr("href")

				if !exists || jobLink == "" {
					return
				}
				if strings.HasPrefix(jobLink, "/") {
					jobLink = "https://emprego.sapo.pt" + jobLink
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
					logoURL = "https://emprego.sapo.pt" + logoURL
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

	log.Printf("[jobs][sapo] Scraped %d jobs", len(vacancies))
	return vacancies
}

// ─────────────────────────────────────────────────────────────────────────────
// Glassdoor Portugal scraper  (lightweight — only public search results)
// ─────────────────────────────────────────────────────────────────────────────

// ScrapeGlassdoorJobs scrapes junior/internship jobs from Glassdoor Portugal.
func ScrapeGlassdoorJobs(allSkills []string, seen map[string]bool) []JsonVacancy {
	log.Println("[jobs] Starting Glassdoor scraper...")

	client := newBrowserClient(30 * time.Second)
	var vacancies []JsonVacancy
	uniqueJobs := make(map[string]*JsonVacancy)
	var orderedKeys []string

	// Glassdoor public search does not paginate well without JS rendering,
	// so we target a few keyword searches on the static HTML endpoint.
	queries := []string{
		"est%C3%A1gio",
		"junior",
		"trainee",
	}

	for _, q := range queries {
		for page := 1; page <= 3; page++ {
			searchURL := fmt.Sprintf(
				"https://www.glassdoor.com/Job/portugal-%s-jobs-SRCH_IL.0,8_IN195_KO9,%d_IP%d.htm",
				q, 9+len(q), page,
			)

			doc, status, err := doGet(client, searchURL)
			if err != nil || status != 200 {
				log.Printf("[jobs][glassdoor] Skipping query '%s' page %d (err=%v status=%d)", q, page, err, status)
				break
			}

			jobsFound := 0

			doc.Find("li.react-job-listing, article.JobCard, div[data-test='jobListing']").Each(func(_ int, s *goquery.Selection) {
				title := strings.TrimSpace(s.Find("[data-test='job-title'], .job-title, a.jobLink").First().Text())
				company := strings.TrimSpace(s.Find("[data-test='employer-name'], .employer-name").First().Text())
				location := strings.TrimSpace(s.Find("[data-test='emp-location'], .location").First().Text())
				jobLink, exists := s.Find("a.jobLink, a[data-test='job-title']").First().Attr("href")

				if !exists || jobLink == "" || title == "" || company == "" {
					return
				}
				if strings.HasPrefix(jobLink, "/") {
					jobLink = "https://www.glassdoor.com" + jobLink
				}
				// Strip tracking params
				if idx := strings.Index(jobLink, "?"); idx != -1 {
					jobLink = jobLink[:idx]
				}

				key := company + "|" + title
				if seen[jobLink] || uniqueJobs[key] != nil {
					return
				}
				seen[jobLink] = true

				jobType, isJunior := determineJobType(title, "")
				if !isJunior {
					isJunior = isLikelyJuniorTitle(title)
					if !isJunior {
						return
					}
					jobType = "junior_position"
				}

				logoURL, _ := s.Find("img.sqLogo, img.logo").First().Attr("src")
				if logoURL == "" {
					logoURL = faviconURL("", company)
				}

				workMode, cleanLoc := detectWorkMode(location, "")
				publishedAt := time.Now().Format("2006-01-02")

				dateText := s.Find("time, [data-test='job-age']").First().AttrOr("datetime", "")
				if dateText != "" {
					publishedAt = dateText
				}

				// Note: Glassdoor detail pages require JS — use the list description snippet if available.
				snippet := strings.TrimSpace(s.Find(".jobDescriptionSnippet, .desc").First().Text())
				desc := "Para mais detalhes, visite o link da candidatura."
				if snippet != "" {
					desc = snippet
				}

				if workMode == "onsite" {
					workMode, _ = detectWorkMode(cleanLoc, desc)
				}

				job := &JsonVacancy{
					CompanyName:       company,
					CompanyUrl:        "",
					CompanyProfileUrl: "",
					Title:             title,
					Region:            strings.TrimSpace(cleanLoc),
					ApplicationUrl:    jobLink,
					Type:              jobType,
					Tags:              extractSkills(title, desc, allSkills),
					Description:       desc,
					WorkMode:          workMode,
					EmploymentType:    "full_time",
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

	log.Printf("[jobs][glassdoor] Scraped %d jobs", len(vacancies))
	return vacancies
}

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// isLikelyJuniorTitle is a broadened pre-check used for platforms where we
// don't have the full description yet. It mirrors the keyword logic in
// determineJobType but is intentionally more permissive.
func isLikelyJuniorTitle(title string) bool {
	t := strings.ToLower(title)
	juniorKeywords := []string{
		"estágio", "estagio", "internship", "trainee", "junior", "júnior",
		"entry level", "entry-level", "recém-licenciado", "recem-licenciado",
		"graduate", "recente",
	}
	for _, kw := range juniorKeywords {
		if strings.Contains(t, kw) {
			return true
		}
	}
	return false
}
