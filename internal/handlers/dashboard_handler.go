package handlers

import (
	"html/template"
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
)

type dashboardData struct {
	BaseURL            string
	AdminKey           string
	FirebaseAPIKey     string
	FirebaseAuthDomain string
	FirebaseProjectID  string
}

// AdminDashboard serves the admin HTML dashboard with config values injected from .env
func AdminDashboard(c *gin.Context) {
	// Derive base URL from the incoming request if not set in config
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	baseURL := scheme + "://" + c.Request.Host + "/api"

	data := dashboardData{
		BaseURL:            baseURL,
		AdminKey:           config.AppConfig.AdminSecretKey,
		FirebaseAPIKey:     config.AppConfig.FirebaseAPIKey,
		FirebaseAuthDomain: config.AppConfig.FirebaseAuthDomain,
		FirebaseProjectID:  config.AppConfig.FirebaseProjectID,
	}

	// Look for the template next to the binary, then next to the source root
	candidates := []string{
		"admin-dashboard.html",
		filepath.Join(filepath.Dir(os.Args[0]), "admin-dashboard.html"),
	}

	var tmplPath string
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			tmplPath = p
			break
		}
	}

	if tmplPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "admin-dashboard.html not found"})
		return
	}

	tmpl, err := template.ParseFiles(tmplPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse dashboard template: " + err.Error()})
		return
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	// Prevent the dashboard from being cached or indexed
	c.Header("Cache-Control", "no-store")
	c.Header("X-Robots-Tag", "noindex")

	if err := tmpl.Execute(c.Writer, data); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Template error: " + err.Error()})
	}
}
