package handlers

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/internal/jobs"
)

// AdminTriggerScrape triggers a LinkedIn vacancy scrape on-demand.
// Intended to be called by Cloud Scheduler (or manually by an admin).
//
// POST /api/admin/scrape-jobs
func AdminTriggerScrape(c *gin.Context) {
	log.Println("[admin] Manual scrape triggered via API")

	// Run in background so the HTTP response returns immediately.
	go func() {
		jobs.ScrapeLinkedInJobs()
	}()

	c.JSON(http.StatusAccepted, gin.H{
		"message": "Scrape iniciado em background. Os resultados serão guardados no GCS.",
	})
}
