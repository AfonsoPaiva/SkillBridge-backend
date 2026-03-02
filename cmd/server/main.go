// Package main SkillBridge API
//
// @title           SkillBridge API
// @version         1.0
// @description     API para a plataforma SkillBridge — liga estudantes e projetos.
// @host            localhost:8080
// @BasePath        /api
//
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
package main

import (
	"log"
	"os"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/middleware"
	"github.com/paiva/SkillBridge/Backend/internal/routes"

	"github.com/gin-gonic/gin"
	_ "github.com/paiva/SkillBridge/Backend/docs" 
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

	// 4. Conectar ao CockroachDB e aplicar migrações
	database.Connect()

	// 5. Criar servidor Gin
	r := gin.Default()

	// 6. Registar todas as rotas
	routes.Setup(r)

	// 7. Iniciar servidor
	port := config.AppConfig.Port
	log.Printf("Servidor Portus a correr em http://localhost:%s", port)
	log.Printf("Ambiente: %s", config.AppConfig.Env)

	if err := r.Run(":" + port); err != nil {
		log.Fatalf("Erro ao iniciar servidor: %v", err)
	}
}
