package config

import (
	_ "embed"
	"encoding/json"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

//go:embed skills.json
var skillsFile []byte

//go:embed UniversidadesECursos.json
var univFile []byte

type SkillSection struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	Skills []string `json:"skills"`
}

// Skills is the predefined list loaded from config/skills.json.
var Skills []string
var SkillSections []SkillSection
var skillsSet map[string]struct{}

// UnivEntry represents a single entry in UniversidadesECursos.json (new format).
type UnivEntry struct {
	Estabelecimento string   `json:"Estabelecimento"`
	TotalCursos     int      `json:"TotalCursos"`
	Cursos          []string `json:"Cursos"`
}

// UnivEntries holds every record from the JSON file.
var UnivEntries []UnivEntry

// UnivList is the deduplicated, alphabetically-sorted slice of establishment names.
var UnivList []string

// CoursesByUniv maps an estabelecimento name to its unique list of cursos.
var CoursesByUniv map[string][]string

func init() {
	// load skills from embedded JSON
	var payload struct {
		Skills   []string       `json:"skills"`
		Sections []SkillSection `json:"sections"`
	}
	if err := json.Unmarshal(skillsFile, &payload); err != nil {
		log.Fatalf("config: failed to parse skills.json: %v", err)
	}

	if len(payload.Sections) > 0 {
		SkillSections = payload.Sections
		Skills = flattenSkillsFromSections(payload.Sections)
	} else {
		Skills = dedupeSkills(payload.Skills)
	}

	skillsSet = make(map[string]struct{}, len(Skills))
	for _, s := range Skills {
		skillsSet[s] = struct{}{}
	}

	// load universities/courses from embedded JSON (new format)
	if err := json.Unmarshal(univFile, &UnivEntries); err != nil {
		log.Fatalf("config: failed to parse UniversidadesECursos.json: %v", err)
	}

	// build deduplicated lists and maps
	CoursesByUniv = make(map[string][]string)
	for _, entry := range UnivEntries {
		name := entry.Estabelecimento
		// Store courses (already unique in the new format)
		CoursesByUniv[name] = entry.Cursos
		UnivList = append(UnivList, name)
	}
	// sort alphabetically for consistent output
	sort.Strings(UnivList)
	for _, courses := range CoursesByUniv {
		sort.Strings(courses)
	}
}

type Config struct {
	Port                    string
	DatabaseURL             string // CockroachDB Cloud full URL (overrides individual params)
	DBHost                  string
	DBPort                  string
	DBUser                  string
	DBPassword              string
	DBName                  string
	DBSSLMode               string
	FirebaseCredentialsPath string
	StripeSecretKey         string
	StripeWebhookSecret     string
	StripeDonationURL       string
	FrontendURL             string   // Base URL of frontend application
	AllowedOrigins          []string // Lista de origens permitidas para CORS (separadas por vírgula)
	BackendURL              string   // Base URL of this backend (for generating absolute image URLs)
	UploadsDir              string
	Env                     string
	GCSBucketName           string // Google Cloud Storage bucket name
	GCSProjectID            string // GCP Project ID
	MailgunAPIKey           string
	MailgunDomain           string
	MailgunSender           string
	ResendAPIKey            string   // Resend API key para envio de emails
	EmailFromAddress        string   // Email "from" para Resend (ex: noreply@skillbridge.pt)
	AdminUIDs               []string // Firebase UIDs com acesso admin
	AdminSecretKey          string   // Chave secreta adicional para rotas de admin (deprecated)
	AdminAllowedIPs         []string // Lista de IPs permitidos para acesso admin (opcional)
	FirebaseAPIKey          string   // Web API Key (para o dashboard admin)
	FirebaseAuthDomain      string   // <project>.firebaseapp.com
	FirebaseProjectID       string   // Project ID
	// Rate limiting
	TOTPMaxAttempts     int // Máximo de tentativas TOTP por período
	TOTPRateLimitWindow int // Janela de tempo em segundos para rate limit
	// reCAPTCHA
	RecaptchaSecretKey string // Google reCAPTCHA v3 secret key (server-side)
	RecaptchaSiteKey   string // Google reCAPTCHA v3 site key (used in logs/docs only)
}

var AppConfig Config

func Load() {
	if err := godotenv.Load(); err != nil {
		log.Println("Aviso: ficheiro .env não encontrado, a usar variáveis de ambiente do sistema.")
	}

	AppConfig = Config{
		Port:                    getEnv("PORT", "8080"),
		DatabaseURL:             getEnv("DATABASE_URL", ""),
		DBHost:                  getEnv("DB_HOST", "localhost"),
		DBPort:                  getEnv("DB_PORT", "26257"),
		DBUser:                  getEnv("DB_USER", "root"),
		DBPassword:              getEnv("DB_PASSWORD", ""),
		DBName:                  getEnv("DB_NAME", "portus_db"),
		DBSSLMode:               getEnv("DB_SSL_MODE", "disable"),
		FirebaseCredentialsPath: getEnv("FIREBASE_CREDENTIALS_PATH", "./config/firebase-credentials.json"),
		StripeSecretKey:         getEnv("STRIPE_SECRET_KEY", ""),
		StripeWebhookSecret:     getEnv("STRIPE_WEBHOOK_SECRET", ""),
		StripeDonationURL:       getEnv("STRIPE_DONATION_URL", "https://donate.stripe.com/test_aFacN44fz2pf5N1fPAenS00"),
		FrontendURL:             getEnv("FRONTEND_URL", "http://localhost:4200"),
		AllowedOrigins:          parseList(getEnv("ALLOWED_ORIGINS", "")),
		BackendURL:              getEnv("BACKEND_URL", ""),
		UploadsDir:              getEnv("UPLOADS_DIR", "./uploads"),
		Env:                     getEnv("ENV", "development"),
		GCSBucketName:           getEnv("GCS_BUCKET_NAME", ""),
		GCSProjectID:            getEnv("GCS_PROJECT_ID", ""),
		MailgunAPIKey:           getEnv("MAILGUN_API_KEY", ""),
		MailgunDomain:           getEnv("MAILGUN_DOMAIN", ""),
		MailgunSender:           getEnv("MAILGUN_SENDER", "noreply@skillbridge.pt"),
		ResendAPIKey:            getEnv("RESEND_API_KEY", ""),
		EmailFromAddress:        getEnv("EMAIL_FROM_ADDRESS", "onboarding@resend.dev"),
		AdminUIDs:               parseList(getEnv("ADMIN_UIDS", "")),
		AdminSecretKey:          getEnv("ADMIN_SECRET_KEY", ""),
		AdminAllowedIPs:         parseList(getEnv("ADMIN_ALLOWED_IPS", "")),
		FirebaseAPIKey:          getEnv("FIREBASE_API_KEY", ""),
		FirebaseAuthDomain:      getEnv("FIREBASE_AUTH_DOMAIN", ""),
		FirebaseProjectID:       getEnv("FIREBASE_PROJECT_ID", ""),
		TOTPMaxAttempts:         getEnvInt("TOTP_MAX_ATTEMPTS", 5),
		TOTPRateLimitWindow:     getEnvInt("TOTP_RATE_LIMIT_WINDOW", 60),
		RecaptchaSecretKey:      getEnv("RECAPTCHA_SECRET_KEY", ""),
		RecaptchaSiteKey:        getEnv("RECAPTCHA_SITE_KEY", ""),
	}
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// getEnvInt retrieves an environment variable as an integer with a fallback.
func getEnvInt(key string, fallback int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
		log.Printf("Warning: Invalid integer value for %s, using default %d", key, fallback)
	}
	return fallback
}

// parseList divide uma string separada por vírgulas em slice, ignorando entradas vazias.
func parseList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		v = strings.TrimSpace(v)
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// IsAdmin verifica se um Firebase UID pertence à lista de administradores.
func IsAdmin(uid string) bool {
	for _, a := range AppConfig.AdminUIDs {
		if a == uid {
			return true
		}
	}
	return false
}

func IsValidSkill(skill string) bool {
	_, ok := skillsSet[skill]
	return ok
}

func flattenSkillsFromSections(sections []SkillSection) []string {
	out := make([]string, 0)
	seen := make(map[string]struct{})
	for _, section := range sections {
		for _, skill := range section.Skills {
			if _, exists := seen[skill]; exists {
				continue
			}
			seen[skill] = struct{}{}
			out = append(out, skill)
		}
	}
	return out
}

func dedupeSkills(skills []string) []string {
	out := make([]string, 0, len(skills))
	seen := make(map[string]struct{}, len(skills))
	for _, skill := range skills {
		if _, exists := seen[skill]; exists {
			continue
		}
		seen[skill] = struct{}{}
		out = append(out, skill)
	}
	return out
}
