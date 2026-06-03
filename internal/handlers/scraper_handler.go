package handlers

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
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

	if isWorkablePage(parsed) {
		jobs, err = scrapeWorkable(parsed)
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
	resp, err := client.Get(pageURL)
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
	resp, err := client.Get(u.String())
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
	jobIDs, err := getWorkableJobIDs(baseURL, u.String())
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
func getWorkableJobIDs(baseURL, pageURL string) ([]string, error) {
	client := &http.Client{Timeout: 15 * time.Second}

	// Try the main page HTML to extract job links
	resp, err := client.Get(pageURL)
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
		resp2, err := client.Get(llmsURL)
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
		feedURL := fmt.Sprintf("https://apply.workable.com/api/v1/widget/accounts/%s", extractSubdomainFromBase(baseURL))
		resp3, err := client.Get(feedURL)
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

func extractSubdomainFromBase(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	// For custom domains, extract from hostname
	host := parsed.Hostname()
	// careers.criticalmanufacturing.com → critical-manufacturing (approximate)
	host = strings.TrimPrefix(host, "careers.")
	host = strings.TrimPrefix(host, "jobs.")
	parts2 := strings.Split(host, ".")
	if len(parts2) > 0 {
		return parts2[0]
	}
	return ""
}

// fetchWorkableJobMarkdown fetches and parses a single job from its Workable markdown endpoint.
func fetchWorkableJobMarkdown(client *http.Client, mdURL string, jobID string, baseURL string) (*ScrapedJob, error) {
	resp, err := client.Get(mdURL)
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

// scrapeGenericHTML tries to scrape job listings from a generic HTML page.
func scrapeGenericHTML(pageURL string) ([]ScrapedJob, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(pageURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500*1024))
	if err != nil {
		return nil, err
	}

	content := string(body)

	// Try to find job listings by searching for common patterns
	var jobs []ScrapedJob

	// Look for <a> tags with job-related href patterns
	reLinks := regexp.MustCompile(`<a[^>]+href="([^"]*(?:job|vaga|career|position|opening)[^"]*)"[^>]*>([^<]+)</a>`)
	matches := reLinks.FindAllStringSubmatch(content, -1)

	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		link := m[1]
		title := strings.TrimSpace(m[2])

		// Check if title matches our keywords
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

		// Resolve relative URLs
		if !strings.HasPrefix(link, "http") {
			base, _ := url.Parse(pageURL)
			ref, _ := url.Parse(link)
			link = base.ResolveReference(ref).String()
		}

		jobs = append(jobs, ScrapedJob{
			Title:          title,
			Type:           classifyVacancyType(titleLower, ""),
			Description:    "",
			ApplicationURL: link,
		})
	}

	return jobs, nil
}
