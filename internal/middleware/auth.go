package middleware

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"google.golang.org/api/option"
)

var firebaseAuth *auth.Client

// InitFirebase inicializa o cliente Firebase Auth
func InitFirebase() {
	var opt option.ClientOption

	// Try to load from env var (Cloud Run with secrets)
	credsContent := os.Getenv("FIREBASE_CREDENTIALS_CONTENT")
	if credsContent != "" {
		opt = option.WithCredentialsJSON([]byte(credsContent))
	} else {
		// Fall back to file path (local development)
		opt = option.WithCredentialsFile(config.AppConfig.FirebaseCredentialsPath)
	}

	app, err := firebase.NewApp(context.Background(), nil, opt)
	if err != nil {
		log.Fatalf("Erro ao inicializar Firebase: %v", err)
	}

	firebaseAuth, err = app.Auth(context.Background())
	if err != nil {
		log.Fatalf("Erro ao obter cliente Firebase Auth: %v", err)
	}

	log.Println("Firebase Auth inicializado com sucesso.")
}

// AuthRequired é o middleware que verifica o token Firebase JWT
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")

		if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token de autenticação em falta."})
			c.Abort()
			return
		}

		idToken := strings.TrimPrefix(authHeader, "Bearer ")

		token, err := firebaseAuth.VerifyIDToken(context.Background(), idToken)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token inválido ou expirado."})
			c.Abort()
			return
		}

		c.Set("firebase_uid", token.UID)
		c.Set("email", token.Claims["email"])
		if name, ok := token.Claims["name"]; ok {
			c.Set("display_name", name)
		}
		// Pass sign_in_provider to identify OAuth accounts
		if firebase, ok := token.Claims["firebase"].(map[string]interface{}); ok {
			if provider, ok := firebase["sign_in_provider"].(string); ok {
				c.Set("sign_in_provider", provider)
			}
		}
		// Extract recruiter custom claims (set by admin during approval)
		if role, ok := token.Claims["role"].(string); ok {
			c.Set("role", role)
		}
		if recruiterID, ok := token.Claims["recruiter_id"].(string); ok {
			c.Set("recruiter_id", recruiterID)
		}
		c.Next()
	}
}

// DeleteUser removes the Firebase Auth user identified by the provided UID.
// This is used when a local account is deleted so the credential is revoked too.
func DeleteUser(uid string) error {
	if firebaseAuth == nil {
		return fmt.Errorf("firebase auth not initialized")
	}
	return firebaseAuth.DeleteUser(context.Background(), uid)
}

// SendPasswordResetEmail sends a password reset email using Firebase Auth native method.
// Firebase handles the email sending automatically using the configured email templates.
func SendPasswordResetEmail(email string) error {
	if firebaseAuth == nil {
		return fmt.Errorf("firebase auth not initialized")
	}

	ctx := context.Background()
	
	// Generate password reset link using Firebase Admin SDK
	// This will trigger Firebase to send the email automatically
	actionCodeSettings := &auth.ActionCodeSettings{
		// URL to redirect after password reset
		// Firebase will append the oobCode parameter automatically
		URL: config.AppConfig.FrontendURL + "/login",
		HandleCodeInApp: false,
	}

	link, err := firebaseAuth.PasswordResetLinkWithSettings(ctx, email, actionCodeSettings)
	if err != nil {
		return fmt.Errorf("erro ao gerar link de reset: %w", err)
	}

	log.Printf("[firebase] Password reset link generated for %s: %s", email, link)
	return nil
}

// RecruiterRequired é o middleware que verifica se o utilizador autenticado é um recrutador.
// Deve ser usado após AuthRequired().
func RecruiterRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		role := c.GetString("role")
		recruiterID := c.GetString("recruiter_id")

		if role != "recruiter" || recruiterID == "" {
			c.JSON(http.StatusForbidden, gin.H{"error": "Acesso restrito a recrutadores."})
			c.Abort()
			return
		}

		c.Next()
	}
}

// CreateFirebaseUser creates a Firebase Auth user for a recruiter and returns the UID.
// If the user already exists (same email), returns the existing UID.
func CreateFirebaseUser(email, displayName string) (string, error) {
	if firebaseAuth == nil {
		return "", fmt.Errorf("firebase auth not initialized")
	}

	ctx := context.Background()

	// Check if user already exists
	userRecord, err := firebaseAuth.GetUserByEmail(ctx, email)
	if err == nil {
		log.Printf("[firebase] User already exists for %s (UID: %s)", email, userRecord.UID)
		return userRecord.UID, nil
	}

	// Create new Firebase user
	params := (&auth.UserToCreate{}).
		Email(email).
		DisplayName(displayName).
		EmailVerified(true)

	userRecord, err = firebaseAuth.CreateUser(ctx, params)
	if err != nil {
		return "", fmt.Errorf("erro ao criar utilizador Firebase: %w", err)
	}

	log.Printf("[firebase] Created user for recruiter %s (UID: %s)", email, userRecord.UID)
	return userRecord.UID, nil
}

// SetRecruiterClaims sets custom claims on a Firebase user to identify them as a recruiter.
func SetRecruiterClaims(uid, recruiterID string) error {
	if firebaseAuth == nil {
		return fmt.Errorf("firebase auth not initialized")
	}

	ctx := context.Background()

	// Get existing claims to merge (avoid overwriting student claims if user is both)
	userRecord, err := firebaseAuth.GetUser(ctx, uid)
	if err != nil {
		return fmt.Errorf("erro ao obter utilizador: %w", err)
	}

	claims := userRecord.CustomClaims
	if claims == nil {
		claims = make(map[string]interface{})
	}
	claims["role"] = "recruiter"
	claims["recruiter_id"] = recruiterID

	if err := firebaseAuth.SetCustomUserClaims(ctx, uid, claims); err != nil {
		return fmt.Errorf("erro ao definir claims: %w", err)
	}

	log.Printf("[firebase] Set recruiter claims for UID=%s (recruiter_id=%s)", uid, recruiterID)
	return nil
}

// GenerateSignInLink generates a Firebase email sign-in link for passwordless auth.
func GenerateSignInLink(emailAddr, continueURL string) (string, error) {
	if firebaseAuth == nil {
		return "", fmt.Errorf("firebase auth not initialized")
	}

	ctx := context.Background()

	settings := &auth.ActionCodeSettings{
		URL:             continueURL,
		HandleCodeInApp: true,
	}

	link, err := firebaseAuth.EmailSignInLink(ctx, emailAddr, settings)
	if err != nil {
		return "", fmt.Errorf("erro ao gerar link de sign-in: %w", err)
	}

	log.Printf("[firebase] Sign-in link generated for %s", emailAddr)
	return link, nil
}
