package handlers

import (
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/models"
)

const (
	APIVersion    = "1.0.0"
	APIName       = "SkillBridge API"
	APIBuildDate  = "2026-03-04"
)

var startTime = time.Now()

// HealthCheck - Verifica o estado de saúde da API e das suas dependências
//
// @Summary      Health Check
// @Description  Verifica se a API está operacional e se consegue conectar à base de dados
// @Tags         system
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Failure      503  {object}  map[string]interface{}
// @Router       /health [get]
func HealthCheck(c *gin.Context) {
	response := gin.H{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"uptime":    time.Since(startTime).String(),
	}

	// Check database connectivity
	if database.DB != nil {
		sqlDB, err := database.DB.DB()
		if err != nil || sqlDB.Ping() != nil {
			response["status"] = "unhealthy"
			response["database"] = "disconnected"
			c.JSON(http.StatusServiceUnavailable, response)
			return
		}
		response["database"] = "connected"
	} else {
		response["status"] = "unhealthy"
		response["database"] = "not initialized"
		c.JSON(http.StatusServiceUnavailable, response)
		return
	}

	c.JSON(http.StatusOK, response)
}

// APIInfo - Devolve informações sobre a API: versão, ambiente, estatísticas, etc.
//
// @Summary      API Information
// @Description  Devolve metadados da API incluindo versão, ambiente, uptime e estatísticas da plataforma
// @Tags         system
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Router       / [get]
// @Router       /api [get]
func APIInfo(c *gin.Context) {
	// Gather platform statistics
	var userCount int64
	var projectCount int64
	var messageCount int64

	database.DB.Model(&models.User{}).Count(&userCount)
	database.DB.Model(&models.Project{}).Count(&projectCount)
	database.DB.Model(&models.Message{}).Count(&messageCount)

	// Get memory stats
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	response := gin.H{
		"name":        APIName,
		"version":     APIVersion,
		"buildDate":   APIBuildDate,
		"environment": config.AppConfig.Env,
		"uptime":      time.Since(startTime).String(),
		"timestamp":   time.Now().UTC().Format(time.RFC3339),
		"server": gin.H{
			"go_version":  runtime.Version(),
			"goroutines":  runtime.NumGoroutine(),
			"memory_mb":   memStats.Alloc / 1024 / 1024,
		},
		"platform": gin.H{
			"users":    userCount,
			"projects": projectCount,
			"messages": messageCount,
		},
		"endpoints": gin.H{
			"api":           config.AppConfig.BackendURL + "/api",
			"health":        config.AppConfig.BackendURL + "/health",
			"documentation": config.AppConfig.BackendURL + "/swagger/index.html",
		},
		"features": []string{
			"E2E Encrypted Messaging",
			"Firebase Authentication",
			"Project Management",
			"User Profiles & Reviews",
			"Skills & University Matching",
			"Stripe Donations",
		},
	}

	c.JSON(http.StatusOK, response)
}
