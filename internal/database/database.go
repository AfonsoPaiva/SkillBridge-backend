package database

import (
	"fmt"
	"log"
	"net/url"
	"strings"

	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/models"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

func Connect() {
	cfg := config.AppConfig

	// CockroachDB Cloud fornece uma DATABASE_URL completa; para instâncias locais
	// constrói-se o DSN a partir das variáveis individuais.
	dsn := cfg.DatabaseURL
	if dsn == "" {
		password := cfg.DBPassword
		if password != "" {
			password = ":" + password
		}
		dsn = fmt.Sprintf(
			"postgresql://%s%s@%s:%s/%s?sslmode=%s&TimeZone=Europe/Lisbon",
			cfg.DBUser, password, cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBSSLMode,
		)
	}

	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Erro ao conectar à base de dados CockroachDB: %v", err)
	}

	log.Println("Conexão ao CockroachDB estabelecida.")

	// Usar sequências normais (1, 2, 3…) em vez do unique_rowid() padrão do CockroachDB
	// que gera IDs de 18 dígitos. Deve ser definido antes de criar as tabelas.
	DB.Exec("SET serial_normalization = 'sql_sequence'")

	if err := migrate(); err != nil {
		log.Fatalf("Erro na migração da base de dados: %v", err)
	}

	// Adicionar colunas novas a tabelas existentes (idempotente)
	alterIfMissing("project_members", "status", "VARCHAR(20) NOT NULL DEFAULT 'pending'")
	alterIfMissing("reviews", "status", "VARCHAR(20) NOT NULL DEFAULT 'pending'")
	alterIfMissing("users", "skills", "JSONB DEFAULT '[]'")
	alterIfMissing("project_roles", "skill_names", "JSONB DEFAULT '[]'")
	migrateProjectRoleSkillNames()
	// Onboarding guest preferences migrated to user profile
	alterIfMissing("users", "role", "VARCHAR(20) NOT NULL DEFAULT ''")
	alterIfMissing("users", "area", "VARCHAR(100) NOT NULL DEFAULT ''")
	alterIfMissing("users", "contact_links", "JSONB DEFAULT '{}'::JSONB")
	// Project image
	alterIfMissing("projects", "image_url", "TEXT DEFAULT ''")
	// Project external links (GitHub, YouTube, gallery, PDF, etc.)
	alterIfMissing("projects", "links", "JSONB DEFAULT '[]'")
	// Project role slots
	alterIfMissing("project_roles", "title", "VARCHAR(200) DEFAULT ''")
	alterIfMissing("project_roles", "spots", "INT NOT NULL DEFAULT 1")
	// Migrate project_roles.filled from BOOL to INT (tracks count, not just flag)
	migrateFilled()
	// Make legacy skill_id column nullable (we now use skill_names JSON field)
	makeNullable("project_roles", "skill_id")
	// Make reviews.project_id nullable (reviews can now be general, not project-specific)
	makeNullable("reviews", "project_id")
	// System message columns
	alterIfMissing("messages", "is_system", "BOOL NOT NULL DEFAULT FALSE")
	alterIfMissing("messages", "message_type", "VARCHAR(50) NOT NULL DEFAULT ''")
	alterIfMissing("messages", "meta_project_id", "INT")
	alterIfMissing("messages", "meta_member_id", "INT")
	alterIfMissing("messages", "meta_status", "VARCHAR(20) NOT NULL DEFAULT ''")
	// TOTP persistent verification timestamp
	alterIfMissing("users", "totp_verified_at", "TIMESTAMP")
	// Email verification status (OAuth accounts are auto-verified)
	alterIfMissing("users", "email_verified", "BOOL NOT NULL DEFAULT FALSE")
	
	// Recruiter fields that were added recently
	alterIfMissing("recruiters", "logo_url", "TEXT DEFAULT ''")
	alterIfMissing("recruiters", "company_url", "TEXT DEFAULT ''")
	alterIfMissing("recruiters", "vacancy_description", "TEXT DEFAULT ''")

	// Vacancy enrichment fields
	alterIfMissing("vacancies", "region", "VARCHAR(100) DEFAULT ''")
	alterIfMissing("vacancies", "work_mode", "VARCHAR(20) DEFAULT ''")
	alterIfMissing("vacancies", "employment_type", "VARCHAR(20) DEFAULT ''")

	migrateFaviconToIconHorse()
}

func migrateFaviconToIconHorse() {
	var recruiters []models.Recruiter
	if err := DB.Where("logo_url LIKE ? OR logo_url LIKE ?", "https://www.google.com/s2/favicons%", "https://ui-avatars.com%").Find(&recruiters).Error; err != nil {
		log.Printf("[migrate] Error finding recruiters with google/ui-avatars favicon: %v", err)
		return
	}
	
	for _, rec := range recruiters {
		domainSafe := strings.ToLower(strings.ReplaceAll(rec.CompanyName, " ", "")) + ".com"
		if rec.CompanyURL != "" {
			if parsedURL, err := url.Parse(rec.CompanyURL); err == nil {
				domain := strings.TrimPrefix(parsedURL.Hostname(), "www.")
				if domain != "" {
					domainSafe = domain
				}
			}
		}
		newLogoURL := "https://icon.horse/icon/" + domainSafe
		if err := DB.Model(&rec).Update("logo_url", newLogoURL).Error; err != nil {
			log.Printf("[migrate] Error updating logo URL for recruiter %s: %v", rec.ID, err)
		} else {
			log.Printf("[migrate] Updated logo URL for recruiter %s to icon.horse", rec.ID)
		}
	}
}

type legacyProjectRoleSkillRow struct {
	ID         uint
	SkillName  string
	SkillNames models.StringList
}

func migrateProjectRoleSkillNames() {
	if !columnExists("project_roles", "skill_names") || !columnExists("project_roles", "skill_name") {
		return
	}

	var roles []legacyProjectRoleSkillRow
	if err := DB.Table("project_roles").Select("id", "skill_name", "skill_names").Find(&roles).Error; err != nil {
		log.Printf("[migrate] Erro ao carregar competências legadas das vagas: %v", err)
		return
	}

	for _, role := range roles {
		legacySkill := strings.TrimSpace(role.SkillName)
		if len(role.SkillNames) > 0 || legacySkill == "" {
			continue
		}

		if err := DB.Table("project_roles").Where("id = ?", role.ID).Update("skill_names", models.StringList{legacySkill}).Error; err != nil {
			log.Printf("[migrate] Erro ao migrar competências da vaga %d: %v", role.ID, err)
		}
	}
}

// alterIfMissing adiciona uma coluna a uma tabela se ela ainda não existir.
func alterIfMissing(table, column, definition string) {
	if !columnExists(table, column) {
		sql := "ALTER TABLE " + table + " ADD COLUMN IF NOT EXISTS " + column + " " + definition
		if err := DB.Exec(sql).Error; err != nil {
			log.Printf("[migrate] Erro ao adicionar coluna %s.%s: %v", table, column, err)
		} else {
			log.Printf("[migrate] Coluna %s.%s adicionada.", table, column)
		}
	}
}

func columnExists(table, column string) bool {
	var count int64
	DB.Raw(
		"SELECT COUNT(*) FROM information_schema.columns WHERE table_name = ? AND column_name = ?",
		table, column,
	).Scan(&count)
	return count > 0
}

// migrateFilled converts project_roles.filled from BOOL to INT if needed.
// CockroachDB requires dropping the column default before changing its type.
func migrateFilled() {
	migrateColToInt4("project_roles", "filled")
	migrateColToInt4("project_roles", "spots")
}

func migrateColToInt4(table, col string) {
	// 1. Check current type
	var dataType string
	DB.Raw(
		"SELECT data_type FROM information_schema.columns WHERE table_name = ? AND column_name = ?",
		table, col,
	).Scan(&dataType)

	if dataType != "boolean" {
		return // already INT or doesn't exist — nothing to do
	}

	log.Printf("[migrate] %s.%s is BOOL — converting to INT4...", table, col)

	steps := []string{
		"ALTER TABLE " + table + " ALTER COLUMN " + col + " DROP DEFAULT",
		"ALTER TABLE " + table + " ALTER COLUMN " + col + " TYPE INT4 USING CASE WHEN " + col + " THEN 1 ELSE 0 END",
		"ALTER TABLE " + table + " ALTER COLUMN " + col + " SET DEFAULT 0",
	}
	for _, sql := range steps {
		if err := DB.Exec(sql).Error; err != nil {
			log.Printf("[migrate] %s: %v", sql, err)
			return
		}
	}
	log.Printf("[migrate] %s.%s convertido para INT4 com sucesso.", table, col)
}

// makeNullable removes a NOT NULL constraint from a column if it currently has one.
func makeNullable(table, col string) {
	var isNullable string
	DB.Raw(
		"SELECT is_nullable FROM information_schema.columns WHERE table_name = ? AND column_name = ?",
		table, col,
	).Scan(&isNullable)
	if isNullable == "" || isNullable == "YES" {
		return // column doesn't exist or is already nullable
	}
	sql := "ALTER TABLE " + table + " ALTER COLUMN " + col + " DROP NOT NULL"
	if err := DB.Exec(sql).Error; err != nil {
		log.Printf("[migrate] Erro ao tornar %s.%s nullable: %v", table, col, err)
	} else {
		log.Printf("[migrate] %s.%s agora é nullable.", table, col)
	}
}

// migrate cria ou atualiza automaticamente as tabelas com base nos modelos
func migrate() error {
	// Apenas cria tabelas que ainda não existem.
	// Usar AutoMigrate em tabelas existentes faz com que o GORM tente renomear
	// constraints (uni_* → idx_*), o que falha no CockroachDB quando a constraint
	// de origem não existe.
	tables := []interface{}{
		&models.User{},
		&models.Project{},
		&models.ProjectOwner{},
		&models.ProjectRole{},
		&models.ProjectMember{},
		&models.Review{},
		&models.Donation{},
		// Messaging
		&models.UserPublicKey{},
		&models.Conversation{},
		&models.Message{},
		&models.PushDeviceToken{},
		// Guest onboarding sessions
		&models.GuestSession{},
		// Follow relationships
		&models.Follow{},
		// Audit logging
		&models.AuditLog{},
		// Recruiter onboarding & vacancies
		&models.Recruiter{},
		&models.Vacancy{},
		&models.RecruiterToken{},
	}
	for _, t := range tables {
		if !DB.Migrator().HasTable(t) {
			if err := DB.Migrator().CreateTable(t); err != nil {
				return err
			}
		}
	}

	// Add slug column to projects if it doesn't exist
	if !DB.Migrator().HasColumn(&models.Project{}, "slug") {
		log.Println("[migrate] Adding slug column to projects table...")
		// Add column as nullable first
		if err := DB.Exec("ALTER TABLE projects ADD COLUMN slug TEXT").Error; err != nil {
			return fmt.Errorf("failed to add slug column: %w", err)
		}

		// Populate slugs for existing projects
		log.Println("[migrate] Populating slugs for existing projects...")
		var projects []models.Project
		if err := DB.Find(&projects).Error; err != nil {
			return fmt.Errorf("failed to fetch projects: %w", err)
		}

		slugCounts := make(map[string]int)
		for _, project := range projects {
			baseSlug := models.GenerateSlug(project.Title)
			slug := baseSlug

			// Ensure uniqueness by appending counter if needed
			if slugCounts[baseSlug] > 0 {
				slug = fmt.Sprintf("%s-%d", baseSlug, slugCounts[baseSlug])
			}
			slugCounts[baseSlug]++

			if err := DB.Model(&models.Project{}).Where("id = ?", project.ID).Update("slug", slug).Error; err != nil {
				log.Printf("[migrate] Warning: failed to set slug for project %d: %v", project.ID, err)
			} else {
				log.Printf("[migrate] Set slug '%s' for project %d", slug, project.ID)
			}
		}

		// Now make it non-null and add unique index
		log.Println("[migrate] Setting slug column as NOT NULL...")
		if err := DB.Exec("ALTER TABLE projects ALTER COLUMN slug SET NOT NULL").Error; err != nil {
			return fmt.Errorf("failed to set slug NOT NULL: %w", err)
		}

		log.Println("[migrate] Creating unique index on slug...")
		if err := DB.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_projects_slug ON projects(slug)").Error; err != nil {
			log.Printf("[migrate] Warning: failed to create slug index: %v", err)
		}

		log.Println("[migrate] Slug column migration completed successfully")
	}

	return nil
}
