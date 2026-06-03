// Package jobs contains background periodic tasks.
package jobs

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"time"

	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/email"
	"github.com/paiva/SkillBridge/Backend/internal/models"
)

// StartVacancyExpiryJob runs a daily check for expired vacancies.
// Expired vacancies are marked as 'expired' and the recruiter receives a renewal email
// with a secure access token that redirects to the vacancy edit page.
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

		// Generate a secure access token for the recruiter with renew param
		token, err := CreateRecruiterToken(vacancy.RecruiterID)
		if err != nil {
			log.Printf("[jobs] Erro ao gerar token de renovação para vaga %s: %v", vacancy.ID, err)
			// Fallback: use frontend URL directly (recruiter will need to sign in manually)
			renewLink := config.AppConfig.FrontendURL + "/recruiter/dashboard"
			email.SendVacancyExpired(vacancy.Recruiter.FullName, vacancy.Recruiter.Email, vacancy.Title, renewLink)
			continue
		}

		renewLink := config.AppConfig.FrontendURL + "/recruiter/auth?token=" + token + "&renew=" + vacancy.ID

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

// CreateRecruiterToken generates a new 72-hour access token for a recruiter.
// Exported so it can be used by both the handler and background jobs.
func CreateRecruiterToken(recruiterID string) (string, error) {
	// Invalidate existing active tokens by setting them to expired
	database.DB.Model(&models.RecruiterToken{}).
		Where("recruiter_id = ? AND expires_at > ?", recruiterID, time.Now()).
		Update("expires_at", time.Now())

	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(bytes)

	rt := models.RecruiterToken{
		RecruiterID: recruiterID,
		Token:       token,
		ExpiresAt:   time.Now().Add(72 * time.Hour),
	}

	if err := database.DB.Create(&rt).Error; err != nil {
		return "", err
	}

	return token, nil
}
