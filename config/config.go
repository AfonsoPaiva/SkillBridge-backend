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

// Skills is the predefined list loaded from config/skills.json.
var Skills []string

// UnivCourse represents a single entry in UniversidadesECursos.json.
type UnivCourse struct {
	Estabelecimento string `json:"Estabelecimento"`
	NomeCurso       string `json:"NomeCurso"`
}

// UnivCourses holds every record from the JSON file (including duplicates).
var UnivCourses []UnivCourse

// UnivList is the deduplicated, alphabetically-sorted slice of establishment names.
var UnivList []string

// CoursesByUniv maps an estabelecimento name to its unique list of cursos.
var CoursesByUniv map[string][]string

func init() {
	// load skills from embedded JSON
	var payload struct {
		Skills []string `json:"skills"`
	}
	if err := json.Unmarshal(skillsFile, &payload); err != nil {
		log.Fatalf("config: failed to parse skills.json: %v", err)
	}
	Skills = payload.Skills

	// load universities/courses from embedded JSON
	if err := json.Unmarshal(univFile, &UnivCourses); err != nil {
		log.Fatalf("config: failed to parse UniversidadesECursos.json: %v", err)
	}

	// build deduplicated lists and maps
	CoursesByUniv = make(map[string][]string)
	for _, uc := range UnivCourses {
		name := uc.Estabelecimento
		course := uc.NomeCurso
		// append course if not already present
		list := CoursesByUniv[name]
		found := false
		for _, existing := range list {
			if existing == course {
				found = true
				break
			}
		}
		if !found {
			CoursesByUniv[name] = append(list, course)
		}
	}

	for name := range CoursesByUniv {
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
	FrontendURL             string // Base URL of frontend application
	BackendURL              string // Base URL of this backend (for generating absolute image URLs)
	UploadsDir              string
	Env                     string
	GCSBucketName           string // Google Cloud Storage bucket name
	GCSProjectID            string // GCP Project ID
	MailgunAPIKey           string
	MailgunDomain           string
	MailgunSender           string
	AdminUIDs               []string // Firebase UIDs com acesso admin
	AdminSecretKey          string   // Chave secreta adicional para rotas de admin (deprecated)
	AdminAllowedIPs         []string // Lista de IPs permitidos para acesso admin (opcional)
	FirebaseAPIKey          string   // Web API Key (para o dashboard admin)
	FirebaseAuthDomain      string   // <project>.firebaseapp.com
	FirebaseProjectID       string   // Project ID
	// Rate limiting
	TOTPMaxAttempts         int      // Máximo de tentativas TOTP por período
	TOTPRateLimitWindow     int      // Janela de tempo em segundos para rate limit
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
		BackendURL:              getEnv("BACKEND_URL", ""),
		UploadsDir:              getEnv("UPLOADS_DIR", "./uploads"),
		Env:                     getEnv("ENV", "development"),
		GCSBucketName:           getEnv("GCS_BUCKET_NAME", ""),
		GCSProjectID:            getEnv("GCS_PROJECT_ID", ""),
		MailgunAPIKey:           getEnv("MAILGUN_API_KEY", ""),
		MailgunDomain:           getEnv("MAILGUN_DOMAIN", ""),
		MailgunSender:           getEnv("MAILGUN_SENDER", "noreply@skillbridge.pt"),
		AdminUIDs:               parseList(getEnv("ADMIN_UIDS", "")),
		AdminSecretKey:          getEnv("ADMIN_SECRET_KEY", ""),
		AdminAllowedIPs:         parseList(getEnv("ADMIN_ALLOWED_IPS", "")),
		FirebaseAPIKey:          getEnv("FIREBASE_API_KEY", ""),
		FirebaseAuthDomain:      getEnv("FIREBASE_AUTH_DOMAIN", ""),
		FirebaseProjectID:       getEnv("FIREBASE_PROJECT_ID", ""),
		TOTPMaxAttempts:         getEnvInt("TOTP_MAX_ATTEMPTS", 5),
		TOTPRateLimitWindow:     getEnvInt("TOTP_RATE_LIMIT_WINDOW", 60),
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
