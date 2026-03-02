package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/auth"
	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"golang.org/x/oauth2/google"
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

// GeneratePasswordResetLink gera um link de redefinição de palavra-passe via Identity Toolkit REST API,
// autenticado com as credenciais de serviço (admin), para que possamos enviar o link por email personalizado.
func GeneratePasswordResetLink(email string) (string, error) {
	ctx := context.Background()

	var credBytes []byte
	var err error

	// Try to load from env var (Cloud Run with secrets)
	credsContent := os.Getenv("FIREBASE_CREDENTIALS_CONTENT")
	if credsContent != "" {
		credBytes = []byte(credsContent)
	} else {
		// Fall back to file path (local development)
		credBytes, err = os.ReadFile(config.AppConfig.FirebaseCredentialsPath)
		if err != nil {
			return "", fmt.Errorf("erro ao ler credenciais Firebase: %w", err)
		}
	}

	creds, err := google.CredentialsFromJSON(ctx, credBytes,
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/identitytoolkit",
	)
	if err != nil {
		return "", fmt.Errorf("erro ao criar credenciais OAuth2: %w", err)
	}

	token, err := creds.TokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("erro ao obter token OAuth2: %w", err)
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"requestType":   "PASSWORD_RESET",
		"email":         email,
		"returnOobLink": true,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://identitytoolkit.googleapis.com/v1/accounts:sendOobCode",
		bytes.NewReader(payload),
	)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("erro na chamada Identity Toolkit: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Identity Toolkit devolveu %d: %s", resp.StatusCode, body)
	}

	var result struct {
		OobLink string `json:"oobLink"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	return result.OobLink, nil
}
