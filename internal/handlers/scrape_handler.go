package handlers

import (
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/internal/jobs"
)

// AdminTriggerScrape triggers a multi-platform vacancy scrape on-demand.
// Intended to be called by Cloud Scheduler (or manually by an admin).
//
// Authentication: header  X-Scrape-Secret: <value of SCRAPE_SECRET env var>
// This avoids needing a Firebase JWT from an automated service.
//
// POST /api/internal/scrape-jobs
func AdminTriggerScrape(c *gin.Context) {
	secret := os.Getenv("SCRAPE_SECRET")
	if secret == "" {
		// Fail-safe: if no secret is configured, deny all automated calls.
		log.Println("[admin] SCRAPE_SECRET not set — rejecting scrape request")
		c.JSON(http.StatusForbidden, gin.H{"error": "Scrape endpoint não configurado."})
		return
	}

	provided := c.GetHeader("X-Scrape-Secret")
	if provided != secret {
		log.Printf("[admin] Scrape request with invalid secret (len=%d)", len(provided))
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Segredo inválido."})
		return
	}

	log.Println("[admin] Scrape triggered via API (Cloud Scheduler or manual)")

	// Run in background so the HTTP response returns immediately.
	go func() {
		jobs.ScrapeLinkedInJobs()
	}()

	c.JSON(http.StatusAccepted, gin.H{
		"message": "Scrape iniciado em background. Os resultados serão guardados no GCS.",
	})
}

