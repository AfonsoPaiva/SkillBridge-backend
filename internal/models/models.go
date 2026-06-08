package models

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ContactLinks armazena links de contacto num campo JSONB.
// Implementa driver.Valuer e sql.Scanner para compatibilidade com GORM/CockroachDB.
type ContactLinks struct {
	Facebook  string `json:"facebook"`
	Instagram string `json:"instagram"`
	Twitter   string `json:"twitter"`
	Tiktok    string `json:"tiktok"`
	LinkedIn  string `json:"linkedin"`
	GitHub    string `json:"github"`
	Website   string `json:"website"`
}

func (cl ContactLinks) Value() (driver.Value, error) {
	return json.Marshal(cl)
}

func (cl *ContactLinks) Scan(value interface{}) error {
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("ContactLinks.Scan: tipo não suportado %T", value)
	}
	return json.Unmarshal(bytes, cl)
}

// StringList stores a JSON array of strings in a single JSONB column.
type StringList []string

func (s StringList) Value() (driver.Value, error) {
	if s == nil {
		return "[]", nil
	}
	b, err := json.Marshal(s)
	return string(b), err
}

func (s *StringList) Scan(value interface{}) error {
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		*s = StringList{}
		return nil
	}
	return json.Unmarshal(bytes, s)
}

// ProjectLink represents a single external link associated with a project.
type ProjectLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Type  string `json:"type"` // github, youtube, gallery, pdf, website, other
}

// ProjectLinks stores a JSON array of ProjectLink in a single JSONB column.
type ProjectLinks []ProjectLink

func (pl ProjectLinks) Value() (driver.Value, error) {
	if pl == nil {
		return "[]", nil
	}
	b, err := json.Marshal(pl)
	return string(b), err
}

func (pl *ProjectLinks) Scan(value interface{}) error {
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		*pl = ProjectLinks{}
		return nil
	}
	return json.Unmarshal(bytes, pl)
}

// --------------------------------------------------
// USER - Utilizador da plataforma
// A password é gerida pelo Firebase (não é armazenada aqui)
// --------------------------------------------------
type User struct {
	ID            uint         `gorm:"primaryKey;autoIncrement" json:"id"`
	FirebaseUID   string       `gorm:"uniqueIndex:idx_users_firebase_uid;not null" json:"firebase_uid"`
	Name          string       `gorm:"not null" json:"name"`
	Slug          string       `gorm:"uniqueIndex:idx_users_slug;not null" json:"slug"`
	Email         string       `gorm:"uniqueIndex:idx_users_email;not null" json:"email"`
	EmailVerified bool         `gorm:"default:false" json:"email_verified"`
	ContactLinks  ContactLinks `gorm:"type:jsonb" json:"contact_links"`
	University    string       `json:"university"`
	Course        string       `json:"course"`
	Year          string       `gorm:"type:varchar(50)" json:"year"`
	Bio           string       `json:"bio"`
	AvatarURL     string       `json:"avatar_url"`
	Skills        StringList   `gorm:"type:jsonb;default:'[]'" json:"skills"`
	// Onboarding preferences — collected on first visit (guest or registered)
	Role string `gorm:"type:varchar(20);default:''" json:"role"` // needs_help | helper
	// TOTP two-factor authentication
	TOTPSecret     string     `gorm:"type:varchar(255);default:''" json:"-"` // TOTP secret (never sent to client)
	TOTPEnabled    bool       `gorm:"default:false" json:"totp_enabled"`
	TOTPVerifiedAt *time.Time `json:"totp_verified_at,omitempty"` // Last successful TOTP verification (persists across restarts)
	CreatedAt      time.Time  `json:"created_at"`

	// Relações
	OwnedProjects  []Project       `gorm:"foreignKey:OwnerID" json:"owned_projects,omitempty"`
	ProjectMembers []ProjectMember `gorm:"foreignKey:UserID" json:"project_members,omitempty"`
}

// --------------------------------------------------
// GUEST_SESSION — Preferências de onboarding para utilizadores anónimos.
// O token UUID é guardado pelo frontend (localStorage) e enviado no registo
// para que as preferências sejam migradas automaticamente para o perfil.
// --------------------------------------------------
type GuestSession struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	Token     string    `gorm:"uniqueIndex:idx_guest_token;not null" json:"token"`
	Role      string    `gorm:"type:varchar(20)" json:"role"` // needs_help | helper
	ExpiresAt time.Time `json:"expires_at"`                   // 90 days from creation
	CreatedAt time.Time `json:"created_at"`
}

// --------------------------------------------------
// PROJECT - Projeto criado por um utilizador
// --------------------------------------------------
type Project struct {
	ID          uint         `gorm:"primaryKey;autoIncrement" json:"id"`
	OwnerID     uint         `gorm:"not null;index" json:"owner_id"`
	Title       string       `gorm:"not null" json:"title"`
	Slug        string       `gorm:"uniqueIndex;not null" json:"slug"`
	Description string       `json:"description"`
	Status      string       `gorm:"type:varchar(20);default:'open'" json:"status"` // open / in_progress / completed
	ImageURL    string       `gorm:"type:text" json:"image_url"`
	Links       ProjectLinks `gorm:"type:jsonb;default:'[]'" json:"links"`
	CreatedAt   time.Time    `json:"created_at"`

	Owner   User            `gorm:"foreignKey:OwnerID" json:"owner,omitempty"`
	Owners  []ProjectOwner  `gorm:"foreignKey:ProjectID" json:"owners,omitempty"`
	Roles   []ProjectRole   `gorm:"foreignKey:ProjectID" json:"roles,omitempty"`
	Members []ProjectMember `gorm:"foreignKey:ProjectID" json:"members,omitempty"`
}

// --------------------------------------------------
// PROJECT_OWNER - Co-proprietários de um projeto
// --------------------------------------------------
type ProjectOwner struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	ProjectID uint      `gorm:"not null;index" json:"project_id"`
	UserID    uint      `gorm:"not null" json:"user_id"`
	AddedAt   time.Time `json:"added_at"`

	User User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// --------------------------------------------------
// PROJECT_ROLE - Perfil que o projeto procura (ex: "Designer UI")
// --------------------------------------------------
type ProjectRole struct {
	ID          uint       `gorm:"primaryKey;autoIncrement" json:"id"`
	ProjectID   uint       `gorm:"not null;index" json:"project_id"`
	Title       string     `gorm:"type:varchar(200)" json:"title"`
	SkillNames  StringList `gorm:"type:jsonb;default:'[]'" json:"skill_names"`
	Description string     `json:"description"`
	Spots       int        `gorm:"type:int4;default:1" json:"spots"`
	Filled      int        `gorm:"type:int4;default:0" json:"filled"`
}

// --------------------------------------------------
// PROJECT_MEMBER - Utilizador que participa num projeto
// Status: pending (candidatura) / accepted (aceite) / rejected (rejeitado)
// --------------------------------------------------
type ProjectMember struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	ProjectID uint      `gorm:"not null;index" json:"project_id"`
	UserID    uint      `gorm:"not null" json:"user_id"`
	RoleID    uint      `json:"role_id"`
	Status    string    `gorm:"type:varchar(20);default:'pending'" json:"status"`
	JoinedAt  time.Time `json:"joined_at"`

	User User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// --------------------------------------------------
// REVIEW - Avaliação entre colaboradores
// --------------------------------------------------
type Review struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	ReviewerID uint      `gorm:"not null;index" json:"reviewer_id"`
	ReviewedID uint      `gorm:"not null;index" json:"reviewed_id"`
	ProjectID  *uint     `json:"project_id,omitempty"`   // opcional - contexto do projeto
	Rating     int       `gorm:"not null" json:"rating"` // 1 a 5
	Comment    string    `json:"comment"`
	Status     string    `gorm:"type:varchar(20);default:'pending'" json:"status"` // pending | approved | rejected
	Reviewer   *User     `gorm:"foreignKey:ReviewerID" json:"reviewer,omitempty"`
	Project    *Project  `gorm:"foreignKey:ProjectID" json:"project,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// --------------------------------------------------
// DONATION - Registo de donativos via Stripe
// --------------------------------------------------
type Donation struct {
	ID              uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	DonorEmail      string    `json:"donor_email"`            // Email do doador (pode ser anónimo)
	Amount          int64     `gorm:"not null" json:"amount"` // Em cêntimos (ex: 500 = 5,00€)
	Currency        string    `gorm:"default:'eur'" json:"currency"`
	StripePaymentID string    `gorm:"uniqueIndex" json:"stripe_payment_id"` // ID do PaymentIntent Stripe
	Status          string    `gorm:"default:'pending'" json:"status"`      // pending / succeeded / failed
	CreatedAt       time.Time `json:"created_at"`
}

// --------------------------------------------------
// MESSAGING — E2E encrypted private messages
//
// The server NEVER stores plaintext. The client generates an X25519 keypair,
// registers the public key here, and encrypts each message with the recipient's
// public key before sending. Only the recipient's private key (kept client-side)
// can decrypt the ciphertext.
//
// Storage estimate: ~700 bytes/message on average (base64 ciphertext + ephemeral
// key + metadata). Easily handles millions of messages per GB of storage.
// --------------------------------------------------

// UserPublicKey holds a user's X25519 public key (base64-encoded).
// One row per user; updated via PUT /messages/keys.
type UserPublicKey struct {
	ID     uint `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID uint `gorm:"uniqueIndex:idx_user_pubkey;not null" json:"user_id"`
	// Base64-encoded raw public key bytes (32 bytes → 44 chars in base64)
	PublicKey string `gorm:"type:text;not null" json:"public_key"`
	// Algorithm used — currently only "X25519"
	Algorithm string    `gorm:"type:varchar(20);default:'X25519'" json:"algorithm"`
	UpdatedAt time.Time `json:"updated_at"`

	User User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// Conversation is a persistent channel between exactly two users.
// UserAID is always the smaller numeric ID so a unique pair is enforced cheaply.
type Conversation struct {
	ID        uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UserAID   uint      `gorm:"not null;uniqueIndex:idx_conv_pair" json:"user_a_id"` // lower  user ID
	UserBID   uint      `gorm:"not null;uniqueIndex:idx_conv_pair" json:"user_b_id"` // higher user ID
	CreatedAt time.Time `json:"created_at"`

	UserA    User      `gorm:"foreignKey:UserAID" json:"user_a,omitempty"`
	UserB    User      `gorm:"foreignKey:UserBID" json:"user_b,omitempty"`
	Messages []Message `gorm:"foreignKey:ConversationID" json:"messages,omitempty"`
}

// --------------------------------------------------
// FOLLOW — Relação de seguidor entre utilizadores
// --------------------------------------------------
type Follow struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	FollowerID  uint      `gorm:"not null;uniqueIndex:idx_follow_pair" json:"follower_id"`
	FollowingID uint      `gorm:"not null;uniqueIndex:idx_follow_pair" json:"following_id"`
	CreatedAt   time.Time `json:"created_at"`

	Follower  User `gorm:"foreignKey:FollowerID" json:"follower,omitempty"`
	Following User `gorm:"foreignKey:FollowingID" json:"following,omitempty"`
}

// Message is a single E2E-encrypted message inside a Conversation.
//
// The client must:
//  1. Generate a random X25519 ephemeral keypair.
//  2. Perform ECDH with the recipient's registered public key → shared secret.
//  3. Derive a symmetric key (HKDF-SHA256) and encrypt with XSalsa20-Poly1305 or AES-GCM.
//  4. Base64-encode (ciphertext) and (ephemeral public key) and POST them here.
//
// The recipient fetches the message, performs ECDH with their private key and the
// stored EphemeralKey to recover the shared secret, then decrypts EncryptedContent.
type Message struct {
	ID             uint `gorm:"primaryKey;autoIncrement" json:"id"`
	ConversationID uint `gorm:"not null;index" json:"conversation_id"`
	SenderID       uint `gorm:"not null;index" json:"sender_id"`
	// Base64-encoded ciphertext — the server cannot read this.
	EncryptedContent string `gorm:"type:text;not null" json:"encrypted_content"`
	// Base64-encoded ephemeral X25519 public key used for this message's ECDH.
	EphemeralKey string `gorm:"type:text;not null" json:"ephemeral_key"`
	// Timestamp the recipient acknowledged the message (nil = unread).
	ReadAt    *time.Time `json:"read_at"`
	CreatedAt time.Time  `json:"created_at"`

	// System-generated messages (e.g., join application notification)
	IsSystem      bool   `gorm:"default:false" json:"is_system"`
	MessageType   string `gorm:"type:varchar(50);default:''" json:"message_type,omitempty"` // 'application'
	MetaProjectID *uint  `json:"meta_project_id,omitempty"`
	MetaMemberID  *uint  `json:"meta_member_id,omitempty"`
	MetaStatus    string `gorm:"type:varchar(20);default:''" json:"meta_status,omitempty"` // 'pending', 'accepted', 'rejected'

	Sender User `gorm:"foreignKey:SenderID" json:"sender,omitempty"`
}

// PushDeviceToken stores device/browser push tokens for Web Push notifications.
// One token can move between users (shared device logout/login), so token must be unique.
type PushDeviceToken struct {
	ID         uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID     uint      `gorm:"not null;index" json:"user_id"`
	Token      string    `gorm:"type:text;uniqueIndex:idx_push_token;not null" json:"token"`
	Platform   string    `gorm:"type:varchar(20);default:'web'" json:"platform"` // web | android | ios
	UserAgent  string    `gorm:"type:text" json:"user_agent"`
	LastSeenAt time.Time `gorm:"index" json:"last_seen_at"`
	CreatedAt  time.Time `json:"created_at"`

	User User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// --------------------------------------------------
// UTILITY FUNCTIONS
// --------------------------------------------------

// GenerateSlug creates a URL-friendly slug from a project title.
// It normalizes accented characters, converts to lowercase,
// replaces spaces/special chars with hyphens, and removes any duplicate hyphens.
func GenerateSlug(title string) string {
	// Normalize accented characters
	slug := normalizeAccents(title)

	// Convert to lowercase
	slug = strings.ToLower(slug)

	// Replace spaces and non-alphanumeric characters with hyphens
	reg := regexp.MustCompile(`[^a-z0-9]+`)
	slug = reg.ReplaceAllString(slug, "-")

	// Remove leading/trailing hyphens
	slug = strings.Trim(slug, "-")

	return slug
}

// normalizeAccents converts accented characters to their ASCII equivalents.
func normalizeAccents(s string) string {
	// Map of accented characters to their ASCII equivalents
	replacements := map[rune]string{
		'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a",
		'À': "A", 'Á': "A", 'Â': "A", 'Ã': "A", 'Ä': "A", 'Å': "A",
		'è': "e", 'é': "e", 'ê': "e", 'ë': "e",
		'È': "E", 'É': "E", 'Ê': "E", 'Ë': "E",
		'ì': "i", 'í': "i", 'î': "i", 'ï': "i",
		'Ì': "I", 'Í': "I", 'Î': "I", 'Ï': "I",
		'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o",
		'Ò': "O", 'Ó': "O", 'Ô': "O", 'Õ': "O", 'Ö': "O",
		'ù': "u", 'ú': "u", 'û': "u", 'ü': "u",
		'Ù': "U", 'Ú': "U", 'Û': "U", 'Ü': "U",
		'ñ': "n", 'Ñ': "N",
		'ç': "c", 'Ç': "C",
		'ý': "y", 'ÿ': "y", 'Ý': "Y",
		'ß': "ss",
		'æ': "ae", 'Æ': "AE",
		'œ': "oe", 'Œ': "OE",
	}

	var result strings.Builder
	for _, char := range s {
		if replacement, found := replacements[char]; found {
			result.WriteString(replacement)
		} else {
			result.WriteRune(char)
		}
	}

	return result.String()
}

// --------------------------------------------------
// AUDIT_LOG - Admin action audit trail
// --------------------------------------------------
type AuditLog struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	FirebaseUID string    `gorm:"index;not null" json:"firebase_uid"`
	Action      string    `gorm:"type:varchar(50);not null" json:"action"`
	Details     string    `json:"details"`
	IPAddress   string    `gorm:"type:varchar(100)" json:"ip_address"`
	UserAgent   string    `json:"user_agent"`
	Timestamp   time.Time `gorm:"index;not null" json:"timestamp"`
}

// --------------------------------------------------
// RECRUITER — Empresa/recrutador que publica vagas na plataforma.
// Autenticação via Firebase Email Link (passwordless).
// Fluxo: apply → pending → approved (admin) → Firebase user criado → acesso.
// --------------------------------------------------
type Recruiter struct {
	ID                 string     `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	FirebaseUID        *string    `gorm:"uniqueIndex:idx_recruiters_firebase_uid" json:"-"`
	FullName           string     `gorm:"not null" json:"full_name"`
	CompanyName        string     `gorm:"not null" json:"company_name"`
	Email              string     `gorm:"uniqueIndex:idx_recruiters_email;not null" json:"email"`
	CompanyURL         string     `gorm:"not null" json:"company_url"`
	CompanyProfileURL  string     `json:"company_profile_url"`
	LogoURL            string     `json:"logo_url"`
	VacancyDescription string     `json:"vacancy_description"`
	Status             string     `gorm:"type:varchar(20);not null;default:'pending_manual'" json:"status"` // pending_manual | pending_auto | approved | rejected
	CreatedAt          time.Time  `json:"created_at"`
	ApprovedAt         *time.Time `json:"approved_at,omitempty"`
}

// --------------------------------------------------
// VACANCY — Vaga publicada por um recrutador.
// Expira automaticamente após 30 dias (cron job diário).
// --------------------------------------------------
type Vacancy struct {
	ID             string     `gorm:"type:uuid;primaryKey;default:gen_random_uuid()" json:"id"`
	RecruiterID    string     `gorm:"type:uuid;not null;index" json:"recruiter_id"`
	Title          string     `gorm:"not null" json:"title"`
	Type           string     `gorm:"type:varchar(30);not null" json:"type"` // summer_internship | curricular_internship | junior_position
	Tags           StringList `gorm:"type:jsonb;not null;default:'[]'" json:"tags"`
	Description    string     `gorm:"not null" json:"description"`
	ApplicationURL string     `gorm:"not null" json:"application_url"`
	Region         string     `gorm:"type:varchar(100);default:''" json:"region"`          // e.g. "Porto, Portugal"
	WorkMode       string     `gorm:"type:varchar(20);default:''" json:"work_mode"`        // hybrid | remote | onsite
	EmploymentType string     `gorm:"type:varchar(20);default:''" json:"employment_type"`  // full_time | part_time
	Deadline       *time.Time `json:"deadline,omitempty"`
	Views          int        `gorm:"default:0" json:"views"`
	Status         string     `gorm:"type:varchar(20);default:'active'" json:"status"` // active | expired | archived
	PublishedAt    time.Time  `gorm:"default:NOW()" json:"published_at"`
	ExpiresAt      time.Time  `json:"expires_at"`

	Recruiter Recruiter `gorm:"foreignKey:RecruiterID" json:"recruiter,omitempty"`
	
	// Transient field for Bulk JSON imports
	CompanyName string `gorm:"-" json:"company_name,omitempty"`
}

// --------------------------------------------------
// RECRUITER_TOKEN — Token de acesso seguro para recrutadores.
// Substitui os Firebase Email Sign-In Links (oobCode) que são de uso único.
// Tokens são válidos por 72 horas e podem ser usados múltiplas vezes.
// --------------------------------------------------
type RecruiterToken struct {
	ID          uint      `gorm:"primaryKey;autoIncrement" json:"id"`
	RecruiterID string    `gorm:"type:uuid;not null;index" json:"recruiter_id"`
	Token       string    `gorm:"type:varchar(64);uniqueIndex:idx_recruiter_token;not null" json:"-"`
	ExpiresAt   time.Time `gorm:"not null" json:"expires_at"`
	UsedCount   int       `gorm:"default:0" json:"used_count"`
	CreatedAt   time.Time `json:"created_at"`

	Recruiter Recruiter `gorm:"foreignKey:RecruiterID" json:"recruiter,omitempty"`
}

