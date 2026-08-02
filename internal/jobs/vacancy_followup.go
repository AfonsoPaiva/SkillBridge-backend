package jobs

import (
	"fmt"
	"log"
	"time"

	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/email"
	"github.com/paiva/SkillBridge/Backend/internal/models"
)

// StartVacancyFollowupJob runs a periodic background check for applications
// submitted >= 7 days ago that have not received a follow-up email.
func StartVacancyFollowupJob() {
	ticker := time.NewTicker(12 * time.Hour)
	log.Println("[jobs] Vacancy application 1-week follow-up job started (runs every 12h)")

	go func() {
		time.Sleep(15 * time.Second)
		checkApplicationFollowups()

		for range ticker.C {
			checkApplicationFollowups()
		}
	}()
}

func checkApplicationFollowups() {
	log.Println("[jobs] Checking for job applications pending 7-day follow-up...")

	sevenDaysAgo := time.Now().Add(-7 * 24 * time.Hour)

	var pendingApps []models.VacancyApplication
	if err := database.DB.Preload("User").Preload("Vacancy.Recruiter").
		Where("applied_at <= ? AND followup_email_sent = false", sevenDaysAgo).
		Find(&pendingApps).Error; err != nil {
		log.Printf("[jobs] Erro ao pesquisar candidaturas pendentes de follow-up: %v", err)
		return
	}

	if len(pendingApps) == 0 {
		log.Println("[jobs] Nenhuma candidatura pendente de follow-up encontrada.")
		return
	}

	log.Printf("[jobs] Encontradas %d candidaturas para envio de email de follow-up", len(pendingApps))

	dashboardURL := config.AppConfig.FrontendURL + "/dashboard"

	for _, app := range pendingApps {
		if app.User.Email == "" {
			continue
		}

		vacancyTitle := "Vaga de Emprego"
		companyName := ""
		if app.Vacancy.Title != "" {
			vacancyTitle = app.Vacancy.Title
		}
		if app.Vacancy.Recruiter.CompanyName != "" {
			companyName = fmt.Sprintf(" na %s", app.Vacancy.Recruiter.CompanyName)
		}

		err := email.SendVacancyApplicationFollowup(
			app.User.Name,
			app.User.Email,
			vacancyTitle,
			companyName,
			dashboardURL,
		)

		if err != nil {
			log.Printf("[jobs] Erro ao enviar email de follow-up para %s: %v", app.User.Email, err)
			continue
		}

		database.DB.Model(&app).Update("followup_email_sent", true)
		log.Printf("[jobs] Email de follow-up enviado a %s para a vaga '%s'", app.User.Email, vacancyTitle)
	}
}
