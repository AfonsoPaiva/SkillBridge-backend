package routes

import (
	"log"
	"strings"

	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/handlers"
	"github.com/paiva/SkillBridge/Backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

func Setup(r *gin.Engine) {

	// Health check endpoint (no authentication required)
	r.GET("/health", handlers.HealthCheck)
	
	// Cache headers middleware for static and public resources
	r.Use(func(c *gin.Context) {
		path := c.Request.URL.Path
		
		// Long cache for static assets (images, fonts, etc.)
		if strings.HasPrefix(path, "/api/upload/") || 
		   strings.HasPrefix(path, "/assets/") ||
		   strings.HasSuffix(path, ".js") || 
		   strings.HasSuffix(path, ".css") ||
		   strings.HasSuffix(path, ".woff") ||
		   strings.HasSuffix(path, ".woff2") ||
		   strings.HasSuffix(path, ".ttf") ||
		   strings.HasSuffix(path, ".svg") ||
		   strings.HasSuffix(path, ".png") ||
		   strings.HasSuffix(path, ".jpg") ||
		   strings.HasSuffix(path, ".jpeg") ||
		   strings.HasSuffix(path, ".webp") {
			// 1 year cache for immutable assets
			c.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else if strings.HasPrefix(path, "/api/") {
			// Short cache for API responses
			c.Header("Cache-Control", "no-cache, must-revalidate")
		}
		
		c.Next()
	})

	// Admin dashboard — protected by IP whitelist (if configured)
	r.GET("/admin-dashboard", middleware.IPWhitelistRequired(), handlers.AdminDashboard)

	// CORS - permite pedidos do frontend Angular (production-safe)
	r.Use(func(c *gin.Context) {
		origin := c.Request.Header.Get("Origin")
		
		// List of allowed origins (always explicitly defined)
		allowedOrigins := []string{
			config.AppConfig.FrontendURL,
			"https://skillbridge-frontend-zeta.vercel.app",
		}
		
		// In development, add localhost
		if config.AppConfig.Env == "development" {
			allowedOrigins = append(allowedOrigins, "http://localhost:4200")
		}
		
		// Check if the origin is allowed (reject if empty or not in list)
		isAllowed := false
		if origin != "" {
			for _, allowed := range allowedOrigins {
				if origin == allowed {
					isAllowed = true
					break
				}
			}
		}
		
		// Log rejected CORS requests for security monitoring
		if !isAllowed && origin != "" && config.AppConfig.Env != "development" {
			log.Printf("[CORS] Rejected request from unauthorized origin: %s (method: %s, path: %s)", 
				origin, c.Request.Method, c.Request.URL.Path)
		}
		
		// Only set CORS headers if origin is explicitly allowed
		if isAllowed {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS, PATCH")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Authorization, Origin, Accept")
			c.Header("Access-Control-Max-Age", "86400") // Cache preflight for 24 hours
		}
		
		// Required for Firebase signInWithPopup to work across same origin
		c.Header("Cross-Origin-Opener-Policy", "same-origin-allow-popups")
		c.Header("Cross-Origin-Embedder-Policy", "unsafe-none")
		
		// Handle preflight OPTIONS request
		if c.Request.Method == "OPTIONS" {
			// Return 204 if allowed, 403 if not
			if isAllowed {
				c.AbortWithStatus(204)
			} else {
				c.AbortWithStatus(403)
			}
			return
		}
		
		c.Next()
	})

	api := r.Group("/api")

	// API information endpoint (no authentication required)
	api.GET("", handlers.APIInfo)
	api.GET("/", handlers.APIInfo)

	// --------------------------------------------------
	// ROTAS PROTEGIDAS (requerem token Firebase)
	// --------------------------------------------------
	protected := api.Group("")
	protected.Use(middleware.AuthRequired())
	{
		// Utilizadores — rotas específicas ANTES do wildcard :id
		protected.POST("/users/register", handlers.RegisterUser)
		protected.GET("/users/me", handlers.GetMyProfile)
		protected.PUT("/users/me", handlers.UpdateProfile)
		protected.DELETE("/users/me", handlers.DeleteMyProfile)
		// Claim an anonymous guest session into the authenticated profile
		protected.POST("/users/me/claim-guest-session", handlers.ClaimGuestSession)

		// Seguidores
		protected.POST("/users/:id/follow", handlers.FollowUser)
		protected.DELETE("/users/:id/follow", handlers.UnfollowUser)
		protected.GET("/users/:id/follow/status", handlers.GetFollowStatus)

		// Skills do utilizador
		protected.POST("/users/me/skills", handlers.AddUserSkill)
		protected.DELETE("/users/me/skills", handlers.RemoveUserSkill)

		// Projetos
		protected.POST("/projects", handlers.CreateProject)
		protected.PUT("/projects/:id", handlers.UpdateProject)
		protected.DELETE("/projects/:id", handlers.DeleteProject)
		protected.PUT("/projects/:id/status", handlers.UpdateProjectStatus)
		protected.POST("/projects/:id/join", handlers.JoinProject)
		protected.GET("/projects/:id/applications", handlers.GetApplications)
		protected.PUT("/projects/:id/applications/:member_id", handlers.RespondApplication)
		protected.DELETE("/projects/:id/members/:member_id", handlers.RemoveProjectMember)
		protected.POST("/projects/:id/owners", handlers.AddProjectOwner)
		protected.POST("/projects/:id/roles", handlers.CreateProjectRole)
		protected.DELETE("/projects/:id/roles/:role_id", handlers.DeleteProjectRole)

		// Avaliações
		protected.POST("/reviews", handlers.CreateReview)

		// Upload de imagens
		protected.POST("/upload/image", handlers.UploadImage)
		protected.DELETE("/upload/image", handlers.DeleteImage)

		// Mensagens privadas E2E cifradas
		protected.PUT("/messages/keys", handlers.RegisterPublicKey)
		protected.GET("/messages/keys/:user_id", handlers.GetPublicKey)
		protected.GET("/messages/unread-count", handlers.GetUnreadCount)
		protected.POST("/conversations", handlers.StartOrGetConversation)
		protected.GET("/conversations", handlers.ListConversations)
		protected.POST("/conversations/:id/messages", handlers.SendMessage)
		protected.GET("/conversations/:id/messages", handlers.GetMessages)
		protected.PUT("/conversations/:id/read", handlers.MarkRead)
	}

	// --------------------------------------------------
	// ROTAS PÚBLICAS (sem autenticação)
	// — registadas DEPOIS das rotas /users/me protegidas
	//   para que o wildcard :id não as intercete primeiro
	// --------------------------------------------------
	public := api.Group("")
	{
		// Utilizadores (público) — wildcard :id deve vir DEPOIS de /me
		public.GET("/users/:id", handlers.GetUserByID)
		public.GET("/users/:id/reviews", handlers.GetUserReviews)
		public.GET("/users/:id/followers", handlers.GetFollowers)
		public.GET("/users/:id/following", handlers.GetFollowing)
		public.GET("/users/:id/follow/counts", handlers.GetFollowCounts)
		// Mot-de-passe (público — não requer token)
		public.POST("/users/password-reset", handlers.RequestPasswordReset)

		// Projetos
		public.GET("/projects", handlers.GetProjects)
		public.GET("/projects/:id", handlers.GetProjectByID)
		public.GET("/projects/:id/members", handlers.GetProjectMembers)

		// Skills (leitura pública)
		public.GET("/skills", handlers.ListSkills)

		// Universidades/cursos estáticos
		public.GET("/universities", handlers.ListUniversities)
		public.GET("/universities/courses", handlers.ListCoursesByUniversity)

		// Guest onboarding sessions (anónimo)
		public.POST("/guest/session", handlers.CreateGuestSession)
		public.GET("/guest/session/:token", handlers.GetGuestSession)
		public.GET("/guest/stats", handlers.GetPlatformStats)

		// Donativos
		public.POST("/donations/embedded-checkout", handlers.CreateEmbeddedCheckoutSession)
		public.POST("/donations/webhook", handlers.StripeWebhook)
		public.GET("/donations/stats", handlers.GetDonationStats)
	}

	// --------------------------------------------------
	// ROTAS DE TOTP (requerem IP whitelist + token Firebase, não TOTP)
	// Estas permitem configurar e verificar TOTP antes de enforcar
	// --------------------------------------------------
	totp := api.Group("/admin/totp")
	totp.Use(middleware.IPWhitelistRequired(), middleware.AuthRequired())
	{
		totp.GET("/status", handlers.TOTPStatus)
		totp.POST("/setup", handlers.TOTPSetup)
		totp.POST("/verify", handlers.TOTPVerify)
		totp.POST("/disable", handlers.TOTPDisable)
	}

	// --------------------------------------------------
	// LIGHTWEIGHT ADMIN CHECK (requires Firebase token only, no IP/TOTP)
	// Used by frontend to determine if user is admin before TOTP flow
	// --------------------------------------------------
	api.GET("/admin/check-access", middleware.AuthRequired(), handlers.AdminCheckAccess)

	// --------------------------------------------------
	// ROTAS DE ADMINISTRAÇÃO (requerem token + UID admin + TOTP válido)
	// --------------------------------------------------
	admin := api.Group("/admin")
	admin.Use(middleware.AuthRequired(), middleware.AdminRequired())
	{
		admin.GET("/users", handlers.AdminListUsers)
		admin.GET("/users/:id", handlers.AdminGetUser)
		admin.DELETE("/users/:id", handlers.AdminDeleteUser)
		admin.GET("/projects", handlers.AdminListProjects)
		admin.DELETE("/projects/:id", handlers.AdminDeleteProject)
		admin.GET("/donations", handlers.AdminListDonations)
		admin.GET("/reviews", handlers.AdminListReviews)
		admin.PUT("/reviews/:id", handlers.AdminDecideReview)
		admin.DELETE("/reviews/:id", handlers.AdminDeleteReview)
		admin.GET("/audit-logs", handlers.AdminGetAuditLogs)
	}
}
