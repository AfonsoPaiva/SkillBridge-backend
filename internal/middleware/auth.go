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
