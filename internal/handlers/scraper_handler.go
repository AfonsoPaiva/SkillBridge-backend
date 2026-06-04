package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ScrapedJob represents a job extracted from a careers page.
type ScrapedJob struct {
	Title          string `json:"title"`
	Type           string `json:"type"`            // summer_internship | curricular_internship | junior_position
	Description    string `json:"description"`
	ApplicationURL string `json:"application_url"`
	Region         string `json:"region"`          // e.g. "Porto, Portugal"
	WorkMode       string `json:"work_mode"`       // hybrid | remote | onsite
	EmploymentType string `json:"employment_type"` // full_time | part_time
}

// juniorKeywords are the title keywords we search for to identify entry-level/internship positions.
var juniorKeywords = []string{
	"junior", "júnior", "jr.", "jr ",
	"intern", "internship", "estágio", "estagio", "estágiário", "estagiário",
	"summer", "verão", "verao",
	"trainee", "graduate", "entry level", "entry-level",
}

// ScrapeVacancies receives a careers page URL and scrapes it for relevant job positions.
// POST /api/recruiter/vacancies/scrape
func ScrapeVacancies(c *gin.Context) {
	var input struct {
		URL string `json:"url" binding:"required"`
	}

	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "URL em falta."})
		return
	}

	// Normalize URL
	careersURL := strings.TrimSpace(input.URL)
	if !strings.HasPrefix(careersURL, "http://") && !strings.HasPrefix(careersURL, "https://") {
		careersURL = "https://" + careersURL
	}

	parsed, err := url.Parse(careersURL)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "URL inválido."})
		return
	}

	log.Printf("[scraper] Scraping careers page: %s", careersURL)

	// Detect platform and scrape accordingly
	var jobs []ScrapedJob

	if !isWorkablePage(parsed) && !isBambooHRPage(parsed) && !isLinkedInPage(parsed) && !isIndeedPage(parsed) {
		if embedded := extractEmbeddedATS(careersURL); embedded != nil {
			log.Printf("[scraper] Found embedded ATS URL: %s", embedded.String())
			parsed = embedded
			careersURL = embedded.String()
		}
	}

	if isLinkedInPage(parsed) {
		jobs, err = scrapeLinkedIn(careersURL)
	} else if isIndeedPage(parsed) {
		jobs, err = scrapeIndeed(careersURL)
	} else if isWorkablePage(parsed) {
		jobs, err = scrapeWorkable(parsed)
	} else if isBambooHRPage(parsed) {
		jobs, err = scrapeBambooHR(parsed)
	} else {
		// For non-Workable pages, try generic HTML scraping
		jobs, err = scrapeGenericHTML(careersURL)
	}

	if err != nil {
		log.Printf("[scraper] Error scraping %s: %v", careersURL, err)
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "Não foi possível extrair vagas desta página. Verifique o URL e tente novamente.",
		})
		return
	}

	if len(jobs) == 0 {
		c.JSON(http.StatusOK, gin.H{
			"jobs":    []ScrapedJob{},
			"count":   0,
			"message": "Não foram encontradas vagas de entrada/estágio nesta página.",
		})
		return
	}

	log.Printf("[scraper] Found %d relevant jobs from %s", len(jobs), careersURL)

	c.JSON(http.StatusOK, gin.H{
		"jobs":  jobs,
		"count": len(jobs),
	})
}

// isWorkablePage checks if a URL is hosted on the Workable platform.
func isWorkablePage(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	// Check for apply.workable.com directly
	if strings.Contains(host, "workable.com") {
		return true
	}
	// Custom domains often proxy to Workable — detect by fetching the page
	// and checking for Workable signatures in the HTML
	return detectWorkableByContent(u.String())
}

// detectWorkableByContent fetches a URL and checks if it's a Workable page.
func detectWorkableByContent(pageURL string) bool {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpGet(client, pageURL)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024)) // Read max 100KB
	if err != nil {
		return false
	}

	content := string(body)
	return strings.Contains(content, "workable.com") ||
		strings.Contains(content, "window.careers") ||
		strings.Contains(content, "dcvxs6ggqztsa.cloudfront.net")
}

// extractWorkableSubdomain extracts the Workable subdomain from either a
// direct workable.com URL or a custom domain page.
func extractWorkableSubdomain(u *url.URL) string {
	host := strings.ToLower(u.Hostname())

	// Direct: apply.workable.com/critical-manufacturing/
	if strings.Contains(host, "workable.com") {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) > 0 && parts[0] != "" {
			return parts[0]
		}
	}

	// Custom domain: careers.criticalmanufacturing.com
	// Try to find subdomain from the page meta tags
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpGet(client, u.String())
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024))
	if err != nil {
		return ""
	}

	content := string(body)

	// Look for: <meta name="subdomain" content="critical-manufacturing">
	re := regexp.MustCompile(`<meta\s+name="subdomain"\s+content="([^"]+)"`)
	matches := re.FindStringSubmatch(content)
	if len(matches) > 1 {
		return matches[1]
	}

	return ""
}

// scrapeWorkable scrapes jobs from a Workable-powered careers page.
func scrapeWorkable(u *url.URL) ([]ScrapedJob, error) {
	subdomain := extractWorkableSubdomain(u)
	if subdomain == "" {
		return nil, fmt.Errorf("could not detect Workable subdomain")
	}

	log.Printf("[scraper] Detected Workable subdomain: %s", subdomain)

	// Determine the base URL for markdown endpoints
	// Custom domain: use the original host
	// Direct workable: use apply.workable.com
	var baseURL string
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, "workable.com") {
		baseURL = fmt.Sprintf("https://apply.workable.com/%s", subdomain)
	} else {
		baseURL = fmt.Sprintf("https://%s", u.Host)
	}

	// Step 1: Get job listings from the llms-full.txt or by scraping the page HTML
	jobIDs, err := getWorkableJobIDs(baseURL, u.String(), subdomain)
	if err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}

	log.Printf("[scraper] Found %d total job IDs", len(jobIDs))

	// Step 2: For each job ID, fetch the markdown details
	var jobs []ScrapedJob
	client := &http.Client{Timeout: 15 * time.Second}

	for _, jobID := range jobIDs {
		mdURL := fmt.Sprintf("%s/_/jobs/view/%s.md", baseURL, jobID)
		job, err := fetchWorkableJobMarkdown(client, mdURL, jobID, baseURL)
		if err != nil {
			log.Printf("[scraper] Error fetching job %s: %v", jobID, err)
			continue
		}

		if job != nil {
			jobs = append(jobs, *job)
		}
	}

	return jobs, nil
}

// getWorkableJobIDs extracts job IDs from a Workable page.
func getWorkableJobIDs(baseURL, pageURL, subdomain string) ([]string, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	// Try the main page HTML to extract job links
	resp, err := httpGet(client, pageURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500*1024))
	if err != nil {
		return nil, err
	}

	content := string(body)

	// Pattern 1: href="/_/j/JOBID/" or href="/j/JOBID/"
	re1 := regexp.MustCompile(`(?:/_)?/j/([A-Z0-9]+)/?`)
	matches1 := re1.FindAllStringSubmatch(content, -1)

	jobIDSet := make(map[string]bool)
	for _, m := range matches1 {
		if len(m) > 1 {
			jobIDSet[m[1]] = true
		}
	}

	// If no job IDs found in main page HTML (SPA), try llms-full.txt
	if len(jobIDSet) == 0 {
		llmsURL := baseURL + "/_/llms-full.txt"
		resp2, err := httpGet(client, llmsURL)
		if err == nil {
			defer resp2.Body.Close()
			if resp2.StatusCode == http.StatusOK {
				body2, _ := io.ReadAll(io.LimitReader(resp2.Body, 1024*1024))
				content2 := string(body2)
				matches2 := re1.FindAllStringSubmatch(content2, -1)
				for _, m := range matches2 {
					if len(m) > 1 {
						jobIDSet[m[1]] = true
					}
				}
			}
		}
	}

	// If still no IDs, try the sitemap or the careers page with different pattern
	if len(jobIDSet) == 0 {
		// Try fetching the workable jobs feed
		feedURL := fmt.Sprintf("https://apply.workable.com/api/v1/widget/accounts/%s", subdomain)
		resp3, err := httpGet(client, feedURL)
		if err == nil {
			defer resp3.Body.Close()
			body3, _ := io.ReadAll(io.LimitReader(resp3.Body, 500*1024))
			content3 := string(body3)
			// Look for shortcode patterns in the API response
			re3 := regexp.MustCompile(`"shortcode"\s*:\s*"([A-Z0-9]+)"`)
			matches3 := re3.FindAllStringSubmatch(content3, -1)
			for _, m := range matches3 {
				if len(m) > 1 {
					jobIDSet[m[1]] = true
				}
			}
		}
	}

	var ids []string
	for id := range jobIDSet {
		ids = append(ids, id)
	}

	return ids, nil
}


// fetchWorkableJobMarkdown fetches and parses a single job from its Workable markdown endpoint.
func fetchWorkableJobMarkdown(client *http.Client, mdURL string, jobID string, baseURL string) (*ScrapedJob, error) {
	resp, err := httpGet(client, mdURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d for %s", resp.StatusCode, mdURL)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 100*1024))
	if err != nil {
		return nil, err
	}

	content := string(body)

	// Parse the markdown header line: "> Company · Location (WorkMode) · EmploymentType · Posted DATE"
	job := parseWorkableMarkdown(content, jobID, baseURL)

	if job == nil {
		return nil, nil // Not a relevant job
	}

	return job, nil
}

// parseWorkableMarkdown parses the structured markdown from Workable's job detail endpoint.
func parseWorkableMarkdown(content string, jobID string, baseURL string) *ScrapedJob {
	lines := strings.Split(content, "\n")

	var title, region, workMode, employmentType, description string

	// Parse the blockquote header line: "> Company · Location (WorkMode) · Employment · Posted DATE"
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "> ") {
			headerContent := strings.TrimPrefix(line, "> ")
			parts := strings.Split(headerContent, " · ")

			if len(parts) >= 2 {
				// Location is usually the second part, may contain work mode in parentheses
				locationPart := strings.TrimSpace(parts[1])
				// Extract work mode from parentheses: "Maia, Portugal (Hybrid)"
				reParens := regexp.MustCompile(`\(([^)]+)\)`)
				parenMatches := reParens.FindStringSubmatch(locationPart)
				if len(parenMatches) > 1 {
					workMode = normalizeWorkMode(parenMatches[1])
					region = strings.TrimSpace(reParens.ReplaceAllString(locationPart, ""))
				} else {
					region = locationPart
				}
			}

			if len(parts) >= 3 {
				employmentType = normalizeEmploymentType(parts[2])
			}
			break
		}
	}

	// Parse **Workplace:** line
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "**Workplace:**") {
			wm := strings.TrimSpace(strings.TrimPrefix(line, "**Workplace:**"))
			if wm != "" {
				workMode = normalizeWorkMode(wm)
			}
		}
	}

	// Extract title from the first heading or the og:title-like content
	// The markdown doesn't have an explicit title heading, but we can
	// use the first bold text or the content before "## Requirements"
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			title = strings.TrimPrefix(line, "# ")
			break
		}
	}

	// If no explicit title, try to extract from the first line (often the title is in the URL/filename)
	if title == "" {
		// Try to extract from content by looking for "The Role" section or a bold title
		re := regexp.MustCompile(`\*\*\s*(?:The Role|O Papel|A Posição)\*\*`)
		if re.MatchString(content) {
			// The title might be in the og:title captured before fetching
			// For now, try to get it from the markdown URL pattern
			title = extractTitleFromContent(content)
		}
	}

	if title == "" {
		// Last resort: the job ID will be used as reference
		title = "Job " + jobID
	}

	// Check if this is a relevant position (junior, intern, etc.)
	titleLower := strings.ToLower(title)
	isRelevant := false
	for _, kw := range juniorKeywords {
		if strings.Contains(titleLower, kw) {
			isRelevant = true
			break
		}
	}

	// Also check description for keywords
	contentLower := strings.ToLower(content)
	if !isRelevant {
		for _, kw := range juniorKeywords {
			if strings.Contains(contentLower, kw) {
				isRelevant = true
				break
			}
		}
	}

	if !isRelevant {
		return nil
	}

	// Check if it's in Portugal
	regionLower := strings.ToLower(region)
	if region != "" && !strings.Contains(regionLower, "portugal") &&
		!strings.Contains(regionLower, "porto") &&
		!strings.Contains(regionLower, "lisboa") &&
		!strings.Contains(regionLower, "lisbon") &&
		!strings.Contains(regionLower, "maia") &&
		!strings.Contains(regionLower, "braga") &&
		!strings.Contains(regionLower, "coimbra") &&
		!strings.Contains(regionLower, "aveiro") &&
		!strings.Contains(regionLower, "faro") &&
		!strings.Contains(regionLower, "funchal") {
		// Also check if region is empty (might be remote)
		if region != "" {
			return nil // Not in Portugal
		}
	}

	// Build description (truncate to 500 chars)
	description = extractDescription(content)
	if len(description) > 500 {
		description = description[:497] + "..."
	}

	// Determine vacancy type based on keywords
	vacancyType := classifyVacancyType(titleLower, contentLower)

	applicationURL := fmt.Sprintf("%s/_/j/%s/", baseURL, jobID)

	return &ScrapedJob{
		Title:          title,
		Type:           vacancyType,
		Description:    description,
		ApplicationURL: applicationURL,
		Region:         region,
		WorkMode:       workMode,
		EmploymentType: employmentType,
	}
}

// extractTitleFromContent tries to extract the job title from markdown content.
func extractTitleFromContent(content string) string {
	// Look for pattern like "As a [Title] at [Company]"
	re := regexp.MustCompile(`(?i)as\s+an?\s+(.+?)\s+at\s+`)
	matches := re.FindStringSubmatch(content)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}

	// Look for the first bold text that looks like a title
	re2 := regexp.MustCompile(`\*\*\s*([^*]+?(?:Engineer|Developer|Designer|Analyst|Manager|Intern|Estagiário|Trainee)[^*]*?)\s*\*\*`)
	matches2 := re2.FindStringSubmatch(content)
	if len(matches2) > 1 {
		return strings.TrimSpace(matches2[1])
	}

	return ""
}

// extractDescription extracts a clean description from the markdown content.
func extractDescription(content string) string {
	lines := strings.Split(content, "\n")
	var descLines []string
	inDescription := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip metadata lines
		if strings.HasPrefix(trimmed, "> ") ||
			strings.HasPrefix(trimmed, "**Workplace:**") ||
			strings.HasPrefix(trimmed, "**Department:**") ||
			strings.HasPrefix(trimmed, "![") ||
			strings.HasPrefix(trimmed, "---") ||
			strings.HasPrefix(trimmed, "## Apply") ||
			strings.HasPrefix(trimmed, "[Apply") ||
			strings.HasPrefix(trimmed, "Powered by") ||
			trimmed == "" && !inDescription {
			continue
		}

		if trimmed == "" && inDescription {
			descLines = append(descLines, "")
			continue
		}

		// Start collecting description
		inDescription = true

		// Stop at requirements section
		if strings.HasPrefix(trimmed, "## Requirements") || strings.HasPrefix(trimmed, "## Apply") {
			break
		}

		// Clean markdown formatting
		cleaned := trimmed
		cleaned = strings.ReplaceAll(cleaned, "**", "")
		cleaned = strings.ReplaceAll(cleaned, "*", "")
		cleaned = strings.TrimPrefix(cleaned, "- ")
		cleaned = strings.TrimPrefix(cleaned, "- ")

		if cleaned != "" {
			descLines = append(descLines, cleaned)
		}
	}

	result := strings.Join(descLines, " ")
	// Clean up multiple spaces
	re := regexp.MustCompile(`\s+`)
	result = re.ReplaceAllString(result, " ")
	return strings.TrimSpace(result)
}

// normalizeWorkMode normalizes work mode strings.
func normalizeWorkMode(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(lower, "hybrid") || strings.Contains(lower, "híbrido") || strings.Contains(lower, "hibrido"):
		return "hybrid"
	case strings.Contains(lower, "remote") || strings.Contains(lower, "remoto"):
		return "remote"
	case strings.Contains(lower, "on-site") || strings.Contains(lower, "onsite") || strings.Contains(lower, "presencial"):
		return "onsite"
	default:
		return lower
	}
}

// normalizeEmploymentType normalizes employment type strings.
func normalizeEmploymentType(raw string) string {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(lower, "full-time") || strings.Contains(lower, "full time") || strings.Contains(lower, "tempo inteiro"):
		return "full_time"
	case strings.Contains(lower, "part-time") || strings.Contains(lower, "part time") || strings.Contains(lower, "tempo parcial"):
		return "part_time"
	case strings.Contains(lower, "contract") || strings.Contains(lower, "contrato"):
		return "contract"
	default:
		return lower
	}
}

// classifyVacancyType determines the vacancy type from title and content.
func classifyVacancyType(titleLower, contentLower string) string {
	// Check for summer internship first
	if strings.Contains(titleLower, "summer") || strings.Contains(titleLower, "verão") ||
		strings.Contains(titleLower, "verao") {
		return "summer_internship"
	}

	// Check for internship/estágio
	if strings.Contains(titleLower, "intern") || strings.Contains(titleLower, "estágio") ||
		strings.Contains(titleLower, "estagio") || strings.Contains(titleLower, "trainee") ||
		strings.Contains(titleLower, "estagiário") || strings.Contains(titleLower, "estágiário") {
		// Check if it's curricular
		if strings.Contains(contentLower, "curricular") {
			return "curricular_internship"
		}
		return "summer_internship"
	}

	// Default to junior position
	return "junior_position"
}

// isBambooHRPage checks if a URL belongs to BambooHR.
func isBambooHRPage(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return strings.Contains(host, "bamboohr.com") || strings.Contains(host, "bamboohr.co.uk")
}

// scrapeBambooHR fetches jobs from BambooHR API.
func scrapeBambooHR(u *url.URL) ([]ScrapedJob, error) {
	apiURL := fmt.Sprintf("https://%s/careers/list", u.Hostname())
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpGet(client, apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bamboohr API returned status %d", resp.StatusCode)
	}

	var result struct {
		Result []struct {
			ID                    string `json:"id"`
			JobOpeningName        string `json:"jobOpeningName"`
			DepartmentLabel       string `json:"departmentLabel"`
			EmploymentStatusLabel string `json:"employmentStatusLabel"`
			Location              struct {
				City  string `json:"city"`
				State string `json:"state"`
			} `json:"location"`
		} `json:"result"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var jobs []ScrapedJob
	var specificJobID string
	pathBase := filepath.Base(u.Path)
	if regexp.MustCompile(`^\d+$`).MatchString(pathBase) {
		specificJobID = pathBase
	}

	for _, j := range result.Result {
		title := j.JobOpeningName

		if specificJobID != "" && specificJobID != j.ID {
			continue
		}

		titleLower := strings.ToLower(title)
		isRelevant := false
		for _, kw := range juniorKeywords {
			if strings.Contains(titleLower, kw) {
				isRelevant = true
				break
			}
		}

		if !isRelevant && specificJobID == "" {
			continue
		}

		jobURL := fmt.Sprintf("https://%s/careers/%s", u.Hostname(), j.ID)
		
		region := j.Location.City
		if j.Location.State != "" && j.Location.State != j.Location.City {
			if region != "" {
				region += ", "
			}
			region += j.Location.State
		}

		jobs = append(jobs, ScrapedJob{
			Title:          title,
			Type:           classifyVacancyType(titleLower, ""),
			ApplicationURL: jobURL,
			Description:    fmt.Sprintf("Department: %s", j.DepartmentLabel),
			Region:         region,
			WorkMode:       "hybrid",
			EmploymentType: normalizeEmploymentType(j.EmploymentStatusLabel),
		})
	}

	return jobs, nil
}

// isLinkedInPage checks if a URL belongs to LinkedIn.
func isLinkedInPage(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return strings.Contains(host, "linkedin.com")
}

// scrapeLinkedIn attempts to scrape a LinkedIn job posting.
func scrapeLinkedIn(pageURL string) ([]ScrapedJob, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", pageURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}
	content := string(body)

	var jobs []ScrapedJob
	reTitle := regexp.MustCompile(`(?i)<meta\s+(?:property|name)="og:title"\s+content="([^"]+)"`)
	titleMatch := reTitle.FindStringSubmatch(content)
	
	title := ""
	if len(titleMatch) > 1 {
		title = titleMatch[1]
		if idx := strings.Index(title, " at "); idx != -1 {
			title = title[:idx]
		}
	} else {
		// Fallback to generic
		return scrapeGenericHTML(pageURL)
	}

	reDesc := regexp.MustCompile(`(?i)<meta\s+(?:property|name)="og:description"\s+content="([^"]+)"`)
	descMatch := reDesc.FindStringSubmatch(content)
	desc := ""
	if len(descMatch) > 1 {
		desc = descMatch[1]
	}

	titleLower := strings.ToLower(title)
	jobs = append(jobs, ScrapedJob{
		Title:          title,
		Type:           classifyVacancyType(titleLower, strings.ToLower(desc)),
		Description:    desc,
		ApplicationURL: pageURL,
	})
	
	return jobs, nil
}

// isIndeedPage checks if a URL belongs to Indeed.
func isIndeedPage(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return strings.Contains(host, "indeed.com") || strings.Contains(host, "indeed.pt")
}

// scrapeIndeed attempts to scrape an Indeed job posting.
func scrapeIndeed(pageURL string) ([]ScrapedJob, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", pageURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, err
	}
	content := string(body)

	var jobs []ScrapedJob
	reTitle := regexp.MustCompile(`(?i)<meta\s+(?:property|name)="og:title"\s+content="([^"]+)"`)
	titleMatch := reTitle.FindStringSubmatch(content)
	
	title := ""
	if len(titleMatch) > 1 {
		title = titleMatch[1]
		if idx := strings.Index(title, " - "); idx != -1 {
			title = title[:idx]
		}
	} else {
		return scrapeGenericHTML(pageURL)
	}

	reDesc := regexp.MustCompile(`(?i)<meta\s+(?:property|name)="og:description"\s+content="([^"]+)"`)
	descMatch := reDesc.FindStringSubmatch(content)
	desc := ""
	if len(descMatch) > 1 {
		desc = descMatch[1]
	}

	titleLower := strings.ToLower(title)
	jobs = append(jobs, ScrapedJob{
		Title:          title,
		Type:           classifyVacancyType(titleLower, strings.ToLower(desc)),
		Description:    desc,
		ApplicationURL: pageURL,
	})
	
	return jobs, nil
}

// scrapeGenericHTML tries to scrape job listings from a generic HTML page.
// It first attempts to detect SPA-based job portals and call their APIs directly,
// then falls back to static HTML parsing.
func scrapeGenericHTML(pageURL string) ([]ScrapedJob, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpGet(client, pageURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500*1024))
	if err != nil {
		return nil, err
	}

	content := string(body)
	parsed, _ := url.Parse(pageURL)
	baseOrigin := fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)

	// 0. Try SPA job search APIs (ESA/Lidl, Greenhouse, Lever, SmartRecruiters)
	if jobs, err := trySPAJobAPIs(client, content, baseOrigin, pageURL); err == nil && len(jobs) > 0 {
		log.Printf("[scraper] SPA API returned %d jobs from %s", len(jobs), pageURL)
		return jobs, nil
	}

	var jobs []ScrapedJob
	seenURLs := make(map[string]bool)

	// 1. Try to find JobPosting JSON-LD schemas (Standard for Google Jobs, used by almost all ATS platforms)
	reLDJSON := regexp.MustCompile(`(?i)<script\s+type=["']application/ld\+json["'][^>]*>([\s\S]*?)</script>`)
	ldMatches := reLDJSON.FindAllStringSubmatch(content, -1)

	for _, m := range ldMatches {
		jsonStr := strings.TrimSpace(m[1])
		if jsonStr == "" {
			continue
		}

		var data interface{}
		if err := json.Unmarshal([]byte(jsonStr), &data); err != nil {
			continue
		}

		extracted := extractJobsFromLDJSON(data, pageURL)
		for _, j := range extracted {
			if !seenURLs[j.ApplicationURL] {
				seenURLs[j.ApplicationURL] = true
				jobs = append(jobs, j)
			}
		}
	}

	// 2. Try to find job listings by searching for common patterns in <a> tags
	if len(jobs) == 0 {
		reLinks := regexp.MustCompile(`(?i)<a[^>]+href="([^"]+)"[^>]*>([^<]+)</a>`)
		matches := reLinks.FindAllStringSubmatch(content, -1)

		for _, m := range matches {
			if len(m) < 3 {
				continue
			}
			link := m[1]
			title := strings.TrimSpace(m[2])

			titleLower := strings.ToLower(title)
			isRelevant := false
			for _, kw := range juniorKeywords {
				if strings.Contains(titleLower, kw) {
					isRelevant = true
					break
				}
			}

			if !isRelevant {
				continue
			}

			if !strings.HasPrefix(link, "http") {
				base, _ := url.Parse(pageURL)
				ref, _ := url.Parse(link)
				link = base.ResolveReference(ref).String()
			}

			if !seenURLs[link] {
				seenURLs[link] = true
				jobs = append(jobs, ScrapedJob{
					Title:          title,
					Type:           classifyVacancyType(titleLower, ""),
					Description:    "",
					ApplicationURL: link,
				})
			}
		}
	}

	// 3. If no valid links or LD-JSON were found (or user provided a direct job link), try page meta tags
	if len(jobs) == 0 {
		reMetaTitle := regexp.MustCompile(`(?i)<meta\s+(?:property|name)="og:title"\s+content="([^"]+)"`)
		metaMatch := reMetaTitle.FindStringSubmatch(content)
		
		pageTitle := ""
		if len(metaMatch) > 1 {
			pageTitle = strings.TrimSpace(metaMatch[1])
		} else {
			reTitle := regexp.MustCompile(`(?i)<title>([^<]+)</title>`)
			titleMatch := reTitle.FindStringSubmatch(content)
			if len(titleMatch) > 1 {
				pageTitle = strings.TrimSpace(titleMatch[1])
			}
		}

		if pageTitle != "" {
			titleLower := strings.ToLower(pageTitle)
			isRelevant := false
			for _, kw := range juniorKeywords {
				if strings.Contains(titleLower, kw) {
					isRelevant = true
					break
				}
			}

			if isRelevant {
				reMetaDesc := regexp.MustCompile(`(?i)<meta\s+(?:property|name)="og:description"\s+content="([^"]+)"`)
				descMatch := reMetaDesc.FindStringSubmatch(content)
				desc := ""
				if len(descMatch) > 1 {
					desc = strings.TrimSpace(descMatch[1])
					if len(desc) > 500 {
						desc = desc[:497] + "..."
					}
				}
				
				jobs = append(jobs, ScrapedJob{
					Title:          pageTitle,
					Type:           classifyVacancyType(titleLower, ""),
					Description:    desc,
					ApplicationURL: pageURL,
				})
			}
		}
	}

	return jobs, nil
}

// trySPAJobAPIs detects SPA-based job portals by analyzing the page HTML
// and calls their internal APIs to get structured job data.
func trySPAJobAPIs(client *http.Client, html string, baseOrigin string, pageURL string) ([]ScrapedJob, error) {
	htmlLower := strings.ToLower(html)

	// Detect ESA platform (Lidl, Kaufland, Schwarz group) - uses search_api/jobsearch
	if strings.Contains(htmlLower, "js_tjobsearch") ||
		strings.Contains(htmlLower, "search_api/jobsearch") ||
		strings.Contains(htmlLower, "jobsearchconfig") ||
		(strings.Contains(htmlLower, "react-container") && strings.Contains(htmlLower, "jobresult")) {
		log.Printf("[scraper] Detected ESA/Lidl-type SPA portal at %s", pageURL)
		return scrapeESAJobAPI(client, baseOrigin, pageURL)
	}

	// Detect Greenhouse embed - uses boards-api.greenhouse.io
	reGH := regexp.MustCompile(`(?i)boards-api\.greenhouse\.io/v1/boards/([a-zA-Z0-9_-]+)`)
	if ghMatch := reGH.FindStringSubmatch(html); len(ghMatch) > 1 {
		log.Printf("[scraper] Detected Greenhouse board: %s", ghMatch[1])
		return scrapeGreenhouseAPI(client, ghMatch[1], pageURL)
	}
	// Also check for Greenhouse iframe embed
	reGHEmbed := regexp.MustCompile(`(?i)grnh\.se/|greenhouse\.io/embed/job_board`)
	if reGHEmbed.MatchString(html) {
		reBoard := regexp.MustCompile(`(?i)boards\.greenhouse\.io/([a-zA-Z0-9_-]+)`)
		if boardMatch := reBoard.FindStringSubmatch(html); len(boardMatch) > 1 {
			return scrapeGreenhouseAPI(client, boardMatch[1], pageURL)
		}
	}

	// Detect Lever - uses jobs.lever.co
	reLever := regexp.MustCompile(`(?i)jobs\.lever\.co/([a-zA-Z0-9_-]+)`)
	if leverMatch := reLever.FindStringSubmatch(html); len(leverMatch) > 1 {
		log.Printf("[scraper] Detected Lever company: %s", leverMatch[1])
		return scrapeLeverAPI(client, leverMatch[1], pageURL)
	}

	// Detect SmartRecruiters - uses jobs.smartrecruiters.com
	reSR := regexp.MustCompile(`(?i)jobs\.smartrecruiters\.com/([a-zA-Z0-9_-]+)`)
	if srMatch := reSR.FindStringSubmatch(html); len(srMatch) > 1 {
		log.Printf("[scraper] Detected SmartRecruiters company: %s", srMatch[1])
		return scrapeSmartRecruitersAPI(client, srMatch[1], pageURL)
	}

	return nil, fmt.Errorf("no SPA API detected")
}

// scrapeESAJobAPI scrapes jobs from the ESA platform (Lidl, Kaufland, etc.)
// by calling the search_api/jobsearch endpoint with pagination.
func scrapeESAJobAPI(client *http.Client, baseOrigin string, pageURL string) ([]ScrapedJob, error) {
	const maxPages = 15
	const resultsPerPage = 50

	var allJobs []ScrapedJob

	for page := 1; page <= maxPages; page++ {
		apiURL := fmt.Sprintf("%s/search_api/jobsearch?type=job&filter=[]&resultsPerPage=%d&page=%d", baseOrigin, resultsPerPage, page)
		log.Printf("[scraper] ESA API page %d: %s", page, apiURL)

		req, err := http.NewRequest("GET", apiURL, nil)
		if err != nil {
			return allJobs, err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Referer", pageURL)

		resp, err := client.Do(req)
		if err != nil {
			if len(allJobs) > 0 {
				return allJobs, nil
			}
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			if len(allJobs) > 0 {
				return allJobs, nil
			}
			return nil, fmt.Errorf("ESA API returned status %d", resp.StatusCode)
		}

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
		if err != nil {
			return allJobs, err
		}

		var apiResp struct {
			Result struct {
				Hits []struct {
					Title              string `json:"title"`
					ContractType       string `json:"contractType"`
					EntryLevel         string `json:"entryLevel"`
					EmploymentAreaTitle string `json:"employmentAreaTitle"`
					JobID              int    `json:"jobId"`
					URL                string `json:"url"`
					Reference          string `json:"reference"`
					SalaryValue        string `json:"salaryValue"`
					RecruitingURL      string `json:"recruitingUrl"`
					DescResp           string `json:"descResponsibilities"`
					Location           struct {
						Title   string `json:"title"`
						City    string `json:"city"`
						Address string `json:"address"`
						Country string `json:"country"`
					} `json:"location"`
				} `json:"hits"`
				Count     int `json:"count"`
				PageCount int `json:"pageCount"`
			} `json:"result"`
		}

		if err := json.Unmarshal(respBody, &apiResp); err != nil {
			if len(allJobs) > 0 {
				return allJobs, nil
			}
			return nil, fmt.Errorf("failed to parse ESA API response: %w", err)
		}

		for _, hit := range apiResp.Result.Hits {
			// Build application URL
			appURL := hit.RecruitingURL
			if appURL == "" && hit.URL != "" {
				appURL = baseOrigin + hit.URL
			}
			if appURL == "" {
				appURL = pageURL
			}

			// Build region from location
			region := hit.Location.City
			if hit.Location.Country != "" && hit.Location.Country != hit.Location.City {
				if region != "" {
					region += ", "
				}
				region += hit.Location.Country
			}

			// Extract short description from HTML description
			desc := stripHTML(hit.DescResp)
			desc = strings.TrimSpace(desc)
			if len(desc) > 500 {
				desc = desc[:497] + "..."
			}

			titleLower := strings.ToLower(hit.Title)
			contentLower := strings.ToLower(hit.Title + " " + hit.EntryLevel + " " + desc)
			vacancyType := classifyVacancyType(titleLower, contentLower)

			allJobs = append(allJobs, ScrapedJob{
				Title:          hit.Title,
				Type:           vacancyType,
				Description:    desc,
				ApplicationURL: appURL,
				Region:         region,
				WorkMode:       "",
				EmploymentType: normalizeEmploymentType(hit.ContractType),
			})
		}

		// Stop if we've fetched all pages
		if page >= apiResp.Result.PageCount || len(apiResp.Result.Hits) == 0 {
			break
		}
	}

	log.Printf("[scraper] ESA API total: %d jobs extracted", len(allJobs))
	return allJobs, nil
}

// scrapeGreenhouseAPI scrapes jobs from a Greenhouse board via their public API.
func scrapeGreenhouseAPI(client *http.Client, boardToken string, pageURL string) ([]ScrapedJob, error) {
	apiURL := fmt.Sprintf("https://boards-api.greenhouse.io/v1/boards/%s/jobs?content=true", boardToken)
	resp, err := httpGet(client, apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Greenhouse API returned status %d", resp.StatusCode)
	}

	var result struct {
		Jobs []struct {
			Title    string `json:"title"`
			AbsURL   string `json:"absolute_url"`
			Content  string `json:"content"`
			Location struct {
				Name string `json:"name"`
			} `json:"location"`
		} `json:"jobs"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var jobs []ScrapedJob
	for _, j := range result.Jobs {
		titleLower := strings.ToLower(j.Title)
		desc := stripHTML(j.Content)
		if len(desc) > 500 {
			desc = desc[:497] + "..."
		}
		contentLower := strings.ToLower(j.Title + " " + desc)

		appURL := j.AbsURL
		if appURL == "" {
			appURL = pageURL
		}

		jobs = append(jobs, ScrapedJob{
			Title:          j.Title,
			Type:           classifyVacancyType(titleLower, contentLower),
			Description:    desc,
			ApplicationURL: appURL,
			Region:         j.Location.Name,
		})
	}

	return jobs, nil
}

// scrapeLeverAPI scrapes jobs from Lever's public API.
func scrapeLeverAPI(client *http.Client, company string, pageURL string) ([]ScrapedJob, error) {
	apiURL := fmt.Sprintf("https://api.lever.co/v0/postings/%s?mode=json", company)
	resp, err := httpGet(client, apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Lever API returned status %d", resp.StatusCode)
	}

	var postings []struct {
		Text        string `json:"text"`
		HostedURL   string `json:"hostedUrl"`
		ApplyURL    string `json:"applyUrl"`
		Description string `json:"descriptionPlain"`
		Categories  struct {
			Location   string `json:"location"`
			Commitment string `json:"commitment"`
		} `json:"categories"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&postings); err != nil {
		return nil, err
	}

	var jobs []ScrapedJob
	for _, p := range postings {
		titleLower := strings.ToLower(p.Text)
		desc := p.Description
		if len(desc) > 500 {
			desc = desc[:497] + "..."
		}

		appURL := p.HostedURL
		if appURL == "" {
			appURL = p.ApplyURL
		}
		if appURL == "" {
			appURL = pageURL
		}

		jobs = append(jobs, ScrapedJob{
			Title:          p.Text,
			Type:           classifyVacancyType(titleLower, strings.ToLower(desc)),
			Description:    desc,
			ApplicationURL: appURL,
			Region:         p.Categories.Location,
			EmploymentType: normalizeEmploymentType(p.Categories.Commitment),
		})
	}

	return jobs, nil
}

// scrapeSmartRecruitersAPI scrapes jobs from SmartRecruiters public API.
func scrapeSmartRecruitersAPI(client *http.Client, company string, pageURL string) ([]ScrapedJob, error) {
	apiURL := fmt.Sprintf("https://api.smartrecruiters.com/v1/companies/%s/postings", company)
	resp, err := httpGet(client, apiURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SmartRecruiters API returned status %d", resp.StatusCode)
	}

	var result struct {
		Content []struct {
			Name string `json:"name"`
			Ref  string `json:"ref"`
			URL  struct {
				API string `json:"api"`
			} `json:"url,omitempty"`
			Location struct {
				City    string `json:"city"`
				Country string `json:"country"`
			} `json:"location"`
		} `json:"content"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var jobs []ScrapedJob
	for _, p := range result.Content {
		titleLower := strings.ToLower(p.Name)
		region := p.Location.City
		if p.Location.Country != "" {
			if region != "" {
				region += ", "
			}
			region += p.Location.Country
		}
		appURL := fmt.Sprintf("https://jobs.smartrecruiters.com/%s/%s", company, p.Ref)

		jobs = append(jobs, ScrapedJob{
			Title:          p.Name,
			Type:           classifyVacancyType(titleLower, ""),
			ApplicationURL: appURL,
			Region:         region,
		})
	}

	return jobs, nil
}

// stripHTML removes basic HTML tags from a string
func stripHTML(content string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	return re.ReplaceAllString(content, "")
}

// extractJobsFromLDJSON recursively searches for JobPosting schemas in decoded JSON-LD
func extractJobsFromLDJSON(data interface{}, pageURL string) []ScrapedJob {
	var jobs []ScrapedJob

	var processObject func(map[string]interface{})
	processObject = func(obj map[string]interface{}) {
		typeVal := obj["@type"]
		isJob := false
		if tStr, ok := typeVal.(string); ok && strings.EqualFold(tStr, "JobPosting") {
			isJob = true
		} else if tArr, ok := typeVal.([]interface{}); ok {
			for _, t := range tArr {
				if tStr, ok := t.(string); ok && strings.EqualFold(tStr, "JobPosting") {
					isJob = true
					break
				}
			}
		}

		if isJob {
			title, _ := obj["title"].(string)
			desc, _ := obj["description"].(string)

			// Extract region
			region := ""
			if loc, ok := obj["jobLocation"].(map[string]interface{}); ok {
				if addr, ok := loc["address"].(map[string]interface{}); ok {
					locality, _ := addr["addressLocality"].(string)
					reg, _ := addr["addressRegion"].(string)
					if locality != "" {
						region = locality
					}
					if reg != "" && reg != locality {
						if region != "" {
							region += ", "
						}
						region += reg
					}
				}
			} else if locArr, ok := obj["jobLocation"].([]interface{}); ok && len(locArr) > 0 {
				if firstLoc, ok := locArr[0].(map[string]interface{}); ok {
					if addr, ok := firstLoc["address"].(map[string]interface{}); ok {
						locality, _ := addr["addressLocality"].(string)
						reg, _ := addr["addressRegion"].(string)
						if locality != "" {
							region = locality
						}
						if reg != "" && reg != locality {
							if region != "" {
								region += ", "
							}
							region += reg
						}
					}
				}
			}

			desc = stripHTML(desc)
			if len(desc) > 500 {
				desc = desc[:497] + "..."
			}

			empTypeStr := ""
			if et, ok := obj["employmentType"].(string); ok {
				empTypeStr = et
			} else if etArr, ok := obj["employmentType"].([]interface{}); ok && len(etArr) > 0 {
				if etStr, ok := etArr[0].(string); ok {
					empTypeStr = etStr
				}
			}

			titleLower := strings.ToLower(title)
			isRelevant := false
			for _, kw := range juniorKeywords {
				if strings.Contains(titleLower, kw) {
					isRelevant = true
					break
				}
			}

			if isRelevant {
				jobs = append(jobs, ScrapedJob{
					Title:          title,
					Type:           classifyVacancyType(titleLower, ""),
					Description:    desc,
					ApplicationURL: pageURL,
					Region:         region,
					EmploymentType: normalizeEmploymentType(empTypeStr),
					WorkMode:       "hybrid",
				})
			}
			return
		}

		// Traverse further
		for _, v := range obj {
			if childObj, ok := v.(map[string]interface{}); ok {
				processObject(childObj)
			} else if childArr, ok := v.([]interface{}); ok {
				for _, item := range childArr {
					if itemObj, ok := item.(map[string]interface{}); ok {
						processObject(itemObj)
					}
				}
			}
		}
	}

	if obj, ok := data.(map[string]interface{}); ok {
		processObject(obj)
	} else if arr, ok := data.([]interface{}); ok {
		for _, item := range arr {
			if obj, ok := item.(map[string]interface{}); ok {
				processObject(obj)
			}
		}
	}

	return jobs
}

// httpGet makes an HTTP GET request with a standard browser User-Agent
func httpGet(client *http.Client, targetURL string) (*http.Response, error) {
	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	return client.Do(req)
}

// extractEmbeddedATS fetches the page and looks for explicit links to known ATS platforms.
func extractEmbeddedATS(pageURL string) *url.URL {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := httpGet(client, pageURL)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500*1024))
	if err != nil {
		return nil
	}
	content := string(body)

	// Look for BambooHR links
	reBamboo := regexp.MustCompile(`(?i)https?://([a-zA-Z0-9.-]+\.bamboohr\.com(?:/careers)?)`)
	if match := reBamboo.FindStringSubmatch(content); len(match) > 1 {
		if u, err := url.Parse("https://" + match[1]); err == nil {
			return u
		}
	}

	// Look for Workable links
	reWorkable := regexp.MustCompile(`(?i)https?://(?:[a-zA-Z0-9.-]*workable\.com|apply\.workable\.com/[a-zA-Z0-9.-]+)`)
	if match := reWorkable.FindString(content); match != "" {
		if u, err := url.Parse(match); err == nil {
			return u
		}
	}

	return nil
}

