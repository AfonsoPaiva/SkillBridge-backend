// Package main SkillBridge API
package main

import (
	"log"
	"os"

	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/email"
	"github.com/paiva/SkillBridge/Backend/internal/handlers"
	"github.com/paiva/SkillBridge/Backend/internal/jobs"
	"github.com/paiva/SkillBridge/Backend/internal/middleware"
	"github.com/paiva/SkillBridge/Backend/internal/notifications"
	"github.com/paiva/SkillBridge/Backend/internal/routes"
	"github.com/paiva/SkillBridge/Backend/internal/storage"

	"github.com/gin-gonic/gin"
)

func main() {
	// 1. Carregar configurações do .env
	config.Load()

	// Production mode: disable debug logging
	if config.AppConfig.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// 2. Criar diretoria de uploads (imagens locais)
	if err := os.MkdirAll(config.AppConfig.UploadsDir, 0755); err != nil {
		log.Fatalf("Erro ao criar diretoria de uploads: %v", err)
	}

	// 3. Inicializar Firebase Auth
	middleware.InitFirebase()

	// 3.1 Inicializar Resend para envio de emails
	if err := email.InitResend(); err != nil {
		log.Printf("[email] Erro ao inicializar Resend: %v", err)
	}

	// 4. Inicializar Google Cloud Storage
	if err := storage.InitGCS(); err != nil {
		log.Printf("⚠ Warning: GCS initialization failed: %v", err)
		log.Println("  Image uploads will not work. Set GCS_BUCKET_NAME and GCS_PROJECT_ID env vars.")
	}
	defer storage.CloseGCS()

	// 4.1 Inicializar Firebase Cloud Messaging (não fatal)
	if err := notifications.InitFirebaseMessaging(); err != nil {
		log.Printf("⚠ Warning: Firebase Cloud Messaging initialization failed: %v", err)
		log.Println("  Push notifications will be disabled until FCM is configured.")
	}

	// 5. Conectar ao CockroachDB e aplicar migrações
	database.Connect()

	// 5.1 Iniciar cron job de expiração de vagas de recrutadores
	jobs.StartVacancyExpiryJob()

	// 5.2 Iniciar cron job para scrape de vagas do LinkedIn
	jobs.StartLinkedInScraperJob()

	// 6. Criar servidor Gin
	r := gin.Default()

	// 7. Registar todas as rotas
	routes.Setup(r)

	// 8. Iniciar servidor
	port := config.AppConfig.Port
	log.Printf("╔══════════════════════════════════════════════════════════╗")
	log.Printf("║  %s v%s", handlers.APIName, handlers.APIVersion)
	log.Printf("║  Build: %s", handlers.APIBuildDate)
	log.Printf("║  Environment: %s", config.AppConfig.Env)
	log.Printf("╠══════════════════════════════════════════════════════════╣")
	log.Printf("║  Server running on: http://localhost:%s", port)
	log.Printf("║  Health Check:      http://localhost:%s/health", port)
	log.Printf("║  API Info:          http://localhost:%s/api", port)
	if config.AppConfig.Env != "production" {
		log.Printf("║  Swagger Docs:      http://localhost:%s/swagger/index.html", port)
	}
	log.Printf("╠══════════════════════════════════════════════════════════╣")
	log.Printf("║  Admin Configuration:")
	log.Printf("║    Allowed UIDs:  %d configured", len(config.AppConfig.AdminUIDs))
	if len(config.AppConfig.AdminAllowedIPs) > 0 {
		log.Printf("║    IP Whitelist:  %d IPs configured", len(config.AppConfig.AdminAllowedIPs))
		for _, ip := range config.AppConfig.AdminAllowedIPs {
			log.Printf("║      - %s", ip)
		}
	} else {
		log.Printf("║    IP Whitelist:  DISABLED (all IPs allowed)")
	}
	log.Printf("╚══════════════════════════════════════════════════════════╝")

	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Erro ao iniciar servidor: %v", err)
	}
}
