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
	// --------------------------------------------------
	// GLOBAL SECURITY LAYER
	// Applied before everything else — order matters!
	// --------------------------------------------------

	// 1. Harden HTTP response headers (HSTS, CSP, anti-clickjacking, etc.)
	r.Use(middleware.SecurityHeaders())

	// 2. Broad DDoS safety net — 300 req/min per IP globally.
	r.Use(middleware.GlobalRateLimit())

	// 3. Reject known scanner/exploit tool User-Agents.
	r.Use(middleware.BotProtection())

	// 4. Block SQL injection and XSS probes in URL paths and query strings.
	r.Use(middleware.SQLInjectionProtection())

	// 5. Cap all API request bodies at 512 KB to prevent memory-exhaustion attacks.
	//    Upload endpoints override this with a higher limit (10 MB).
	r.Use(middleware.LimitBodySize(middleware.DefaultBodyLimit))

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

		// Get allowed origins from config (comma-separated in ALLOWED_ORIGINS env var)
		allowedOrigins := config.AppConfig.AllowedOrigins

		// Fallback to FrontendURL if ALLOWED_ORIGINS is not set
		if len(allowedOrigins) == 0 && config.AppConfig.FrontendURL != "" {
			allowedOrigins = []string{config.AppConfig.FrontendURL}
		}

		// In development, add localhost
		if config.AppConfig.Env == "development" {
			allowedOrigins = append(allowedOrigins, "http://localhost:4200")
		}

		// Check if the origin is allowed (reject if empty or not in list)
		// Also allow all *.vercel.app subdomains for preview deployments
		isAllowed := false
		if origin != "" {
			for _, allowed := range allowedOrigins {
				if origin == allowed {
					isAllowed = true
					break
				}
			}
			// Allow Vercel preview deployments (*.vercel.app)
			if !isAllowed && strings.HasSuffix(origin, ".vercel.app") && strings.HasPrefix(origin, "https://skillbridge.pt") {
				isAllowed = true
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
		// Email verification
		protected.POST("/users/me/verify-email", handlers.UpdateEmailVerification)
		protected.GET("/users/me/email-verified", handlers.CheckEmailVerification)

		// Seguidores
		protected.POST("/users/:id/follow", handlers.FollowUser)
		protected.DELETE("/users/:id/follow", handlers.UnfollowUser)
		protected.GET("/users/:id/follow/status", handlers.GetFollowStatus)

		// Skills do utilizador
		protected.POST("/users/me/skills", handlers.AddUserSkill)
		protected.DELETE("/users/me/skills", handlers.RemoveUserSkill)

		// Vagas favoritas e candidaturas
		protected.POST("/vacancies/:id/favorite", middleware.SensitiveWriteRateLimit(), handlers.ToggleFavoriteVacancy)
		protected.GET("/vacancies/favorites/me", handlers.GetMyFavoriteVacancies)
		protected.GET("/vacancies/applications/me", handlers.GetMyVacancyApplications)
		protected.POST("/vacancies/:id/apply", middleware.SensitiveWriteRateLimit(), handlers.ApplyToVacancy)
		protected.DELETE("/vacancies/:id/apply", middleware.SensitiveWriteRateLimit(), handlers.RemoveApplication)
		protected.POST("/vacancies/:id/application-status", middleware.SensitiveWriteRateLimit(), handlers.UpdateApplicationStatus)

		// Projetos
		protected.POST("/projects", handlers.CreateProject)
		protected.PUT("/projects/:id", handlers.UpdateProject)
		protected.DELETE("/projects/:id", handlers.DeleteProject)
		protected.PUT("/projects/:id/status", handlers.UpdateProjectStatus)
		protected.POST("/projects/:id/join", handlers.JoinProject)
		protected.GET("/projects/:id/applications", handlers.GetApplications)
		protected.GET("/projects/:id/my-applications", handlers.GetMyApplications)
		protected.PUT("/projects/:id/applications/:member_id", handlers.RespondApplication)
		protected.DELETE("/projects/:id/members/:member_id", handlers.RemoveProjectMember)
		protected.POST("/projects/:id/owners", handlers.AddProjectOwner)
		protected.POST("/projects/:id/roles", handlers.CreateProjectRole)
		protected.DELETE("/projects/:id/roles/:role_id", handlers.DeleteProjectRole)

		// Avaliações
		protected.POST("/reviews", handlers.CreateReview)
		protected.POST("/universities/reviews", handlers.CreateUniversityReview)

		// Upload de imagens — higher body limit for image files (10 MB)
		protected.POST("/upload/image", middleware.LimitBodySize(middleware.UploadBodyLimit), handlers.UploadImage)
		protected.DELETE("/upload/image", handlers.DeleteImage)

		// Mensagens privadas E2E cifradas
		protected.PUT("/messages/keys", handlers.RegisterPublicKey)
		protected.GET("/messages/keys/:user_id", handlers.GetPublicKey)
		protected.GET("/messages/unread-count", handlers.GetUnreadCount)
		protected.POST("/notifications/tokens", handlers.RegisterPushToken)
		protected.DELETE("/notifications/tokens", handlers.DeletePushToken)
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
		// Mot-de-passe (público — não requer token) — rate-limited + reCAPTCHA (10 req/min)
		public.POST("/users/password-reset", middleware.AuthFlowRateLimit(), middleware.VerifyRecaptcha(0.5), handlers.RequestPasswordReset)

		// Projetos — list is rate-limited (60 req/min)
		public.GET("/projects", middleware.HeavyReadRateLimit(), handlers.GetProjects)
		public.GET("/projects/:id", handlers.GetProjectByID)
		public.GET("/projects/:id/members", handlers.GetProjectMembers)

		// Skills (leitura pública)
		public.GET("/skills", handlers.ListSkills)

		// Universidades/cursos estáticos e avaliações
		public.GET("/universities", handlers.ListUniversities)
		public.GET("/universities/search", handlers.SearchUniversities)
		public.GET("/universities/courses", handlers.ListCoursesByUniversity)
		public.GET("/universities/rankings", handlers.GetUniversityRankings)
		public.GET("/universities/reviews", handlers.GetUniversityReviews)

		// Guest onboarding sessions (anónimo) — write limited + reCAPTCHA (20 req/min)
		public.POST("/guest/session", middleware.SensitiveWriteRateLimit(), middleware.VerifyRecaptcha(0.3), handlers.CreateGuestSession)
		public.GET("/guest/session/:token", handlers.GetGuestSession)
		public.GET("/guest/stats", handlers.GetPlatformStats)

		// Donativos
		public.POST("/donations/embedded-checkout", middleware.DonationCheckoutRateLimit(), handlers.CreateEmbeddedCheckoutSession)
		public.POST("/donations/webhook", handlers.StripeWebhook)
		public.GET("/donations/stats", handlers.GetDonationStats)

		// Proxy de Imagens — rate-limited to 120 req/min (cache-miss path)
		public.GET("/proxy/image", middleware.ProxyImageRateLimit(), handlers.ProxyImage)

		// Build Challenge — public stats
		// TODO: Build Challenge desativado temporariamente (ninguém se inscreveu)
		// public.GET("/contest/stats", handlers.ContestGetStats)
	}

	// --------------------------------------------------
	// ROTAS DO CONCURSO (requerem token Firebase)
	// TODO: Build Challenge desativado temporariamente (ninguém se inscreveu)
	// --------------------------------------------------
	// contest := api.Group("/contest")
	// contest.Use(middleware.AuthRequired())
	// {
	// 	contest.POST("/register", middleware.SensitiveWriteRateLimit(), handlers.ContestRegister)
	// 	contest.GET("/registrations/me", handlers.ContestGetMyRegistration)
	// 	contest.GET("/my-projects", handlers.ContestGetUserProjects)
	// }

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
		admin.PUT("/users/:id", handlers.AdminUpdateUser)
		admin.DELETE("/users/:id", handlers.AdminDeleteUser)
		admin.GET("/projects", handlers.AdminListProjects)
		admin.PUT("/projects/:id", handlers.AdminUpdateProject)
		admin.DELETE("/projects/:id", handlers.AdminDeleteProject)
		admin.GET("/donations", handlers.AdminListDonations)
		admin.GET("/reviews", handlers.AdminListReviews)
		admin.PUT("/reviews/:id", handlers.AdminDecideReview)
		admin.DELETE("/reviews/:id", handlers.AdminDeleteReview)
		admin.GET("/audit-logs", handlers.AdminGetAuditLogs)
		admin.POST("/clean-unused-images", handlers.AdminCleanUnusedImages)
		admin.POST("/send-marketing-email", handlers.AdminSendMarketingEmail)
		admin.GET("/university-stats", handlers.AdminGetUniversityStats)
		admin.GET("/university-reviews", handlers.AdminListUniversityReviews)
		admin.DELETE("/university-reviews/:id", handlers.AdminDeleteUniversityReview)
		// Recruiter management
		admin.GET("/recruiters", handlers.AdminListRecruiters)
		admin.GET("/recruiters/pending-count", handlers.AdminGetPendingRecruitersCount)
		admin.POST("/recruiters/:id/approve", handlers.AdminApproveRecruiter)
		admin.POST("/recruiters/:id/reject", handlers.AdminRejectRecruiter)
		admin.DELETE("/recruiters/:id", handlers.AdminDeleteRecruiter)
		admin.POST("/recruiters/:id/resend-email", handlers.AdminResendRecruiterEmail)
		// Vacancy management

		// Contest management
		// TODO: Build Challenge desativado temporariamente (ninguém se inscreveu)
		// admin.GET("/contest-registrations", handlers.AdminListContestRegistrations)
		// admin.PUT("/contest-registrations/:id", handlers.AdminUpdateContestRegistration)
		// admin.DELETE("/contest-registrations/:id", handlers.AdminDeleteContestRegistration)
	}

	// --------------------------------------------------
	// ROTAS INTERNAS — Cloud Scheduler / cron jobs
	// Sem Firebase: autenticadas apenas por X-Scrape-Secret header
	// (segredo definido na variável de ambiente SCRAPE_SECRET)
	// --------------------------------------------------
	internal := api.Group("/internal")
	{
		// Cloud Scheduler invoca este endpoint para actualizar as vagas de emprego.
		// O handler valida o header X-Scrape-Secret internamente.
		internal.POST("/scrape-jobs", handlers.AdminTriggerScrape)
	}
	// --------------------------------------------------
	// ROTAS DE RECRUTADORES (públicas)
	// --------------------------------------------------
	// Recruiter public write endpoints — rate-limited + reCAPTCHA on high-risk paths
	public.POST("/recruiters/apply", middleware.SensitiveWriteRateLimit(), middleware.VerifyRecaptcha(0.5), handlers.RecruiterApply)
	public.POST("/recruiters/request-link", middleware.AuthFlowRateLimit(), middleware.VerifyRecaptcha(0.5), handlers.RecruiterRequestLink)
	public.POST("/recruiters/verify-token", middleware.AuthFlowRateLimit(), handlers.RecruiterVerifyToken)
	// Vacancies list — heavy DB read, rate-limited (60 req/min)
	public.GET("/vacancies", middleware.HeavyReadRateLimit(), handlers.GetPublicVacancies)
	public.GET("/vacancies/community-stats", handlers.GetCommunityVacancyStats)
	public.GET("/vacancies/:id", handlers.GetPublicVacancy)

	// --------------------------------------------------
	// ROTAS DE RECRUTADORES (autenticadas — Firebase + role recruiter)
	// --------------------------------------------------
	recruiter := api.Group("/recruiter")
	recruiter.Use(middleware.AuthRequired(), middleware.RecruiterRequired())
	{
		recruiter.GET("/profile", handlers.GetRecruiterProfile)
		recruiter.PUT("/profile", handlers.UpdateRecruiterProfile)
		recruiter.DELETE("/profile", handlers.DeleteRecruiterProfile)
		recruiter.GET("/vacancies", handlers.ListMyVacancies)
		recruiter.POST("/vacancies", handlers.CreateVacancy)
		recruiter.PUT("/vacancies/:id", handlers.UpdateVacancy)
		recruiter.DELETE("/vacancies/:id", handlers.DeleteVacancy)
		recruiter.DELETE("/vacancies/:id/permanent", handlers.PermanentDeleteVacancy)
	}
}
