// Package jobs contains background periodic tasks.
package jobs

import (
	"log"
	"net/url"
	"time"

	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/email"
	"github.com/paiva/SkillBridge/Backend/internal/middleware"
	"github.com/paiva/SkillBridge/Backend/internal/models"
)

// StartVacancyExpiryJob runs a daily check for expired vacancies.
// Expired vacancies are marked as 'expired' and the recruiter receives a renewal email
// with a Firebase sign-in link that redirects to the vacancy edit page.
func StartVacancyExpiryJob() {
	ticker := time.NewTicker(24 * time.Hour)
	log.Println("[jobs] Vacancy expiry job started (runs every 24h)")

	go func() {
		// Run once on startup (with a small delay to allow DB initialization)
		time.Sleep(10 * time.Second)
		checkExpiredVacancies()

		for range ticker.C {
			checkExpiredVacancies()
		}
	}()
}

func checkExpiredVacancies() {
	log.Println("[jobs] Checking for expired vacancies...")

	var vacancies []models.Vacancy
	if err := database.DB.
		Preload("Recruiter").
		Where("expires_at < ? AND status = ?", time.Now(), "active").
		Find(&vacancies).Error; err != nil {
		log.Printf("[jobs] Erro ao buscar vagas expiradas: %v", err)
		return
	}

	if len(vacancies) == 0 {
		log.Println("[jobs] Nenhuma vaga expirada encontrada.")
		return
	}

	log.Printf("[jobs] Encontradas %d vagas expiradas", len(vacancies))

	for _, vacancy := range vacancies {
		// Mark as expired
		if err := database.DB.Model(&vacancy).Update("status", "expired").Error; err != nil {
			log.Printf("[jobs] Erro ao expirar vaga %s: %v", vacancy.ID, err)
			continue
		}

		// Generate a new sign-in link for the recruiter with renew param
		continueURL := config.AppConfig.FrontendURL + "/recruiter/auth?email=" +
			url.QueryEscape(vacancy.Recruiter.Email) + "&renew=" + vacancy.ID

		renewLink, err := middleware.GenerateSignInLink(vacancy.Recruiter.Email, continueURL)
		if err != nil {
			log.Printf("[jobs] Erro ao gerar link de renovação para vaga %s: %v", vacancy.ID, err)
			// Fallback: use frontend URL directly (recruiter will need to sign in manually)
			renewLink = config.AppConfig.FrontendURL + "/recruiter/dashboard"
		}

		// Send expiry notification email
		if err := email.SendVacancyExpired(
			vacancy.Recruiter.FullName,
			vacancy.Recruiter.Email,
			vacancy.Title,
			renewLink,
		); err != nil {
			log.Printf("[jobs] Erro ao enviar email de expiração para vaga %s: %v", vacancy.ID, err)
		}

		log.Printf("[jobs] Vaga expirada: %s (%s)", vacancy.Title, vacancy.ID)
	}
}
