package middleware

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Google reCAPTCHA v3 verification middleware
// ---------------------------------------------------------------------------
//
// Backend verifies every reCAPTCHA token sent by the frontend before allowing
// write operations to proceed.  This protects public endpoints (recruiter
// apply, guest session, password-reset) from automated bot submissions.
//
// Usage in routes.go:
//
//	public.POST("/recruiters/apply",
//	    middleware.SensitiveWriteRateLimit(),
//	    middleware.VerifyRecaptcha(0.5),   // score threshold 0–1
//	    handlers.RecruiterApply)
//
// The frontend must:
//  1. Load the reCAPTCHA v3 script with the site key (see index.html).
//  2. Call `grecaptcha.execute(siteKey, {action: 'submit'})` to get a token.
//  3. Send the token in the JSON body as `"recaptcha_token": "<token>"`.
//
// ---------------------------------------------------------------------------

const recaptchaVerifyURL = "https://www.google.com/recaptcha/api/siteverify"

// recaptchaResponse is the shape returned by Google's verify endpoint.
type recaptchaResponse struct {
	Success     bool     `json:"success"`
	Score       float64  `json:"score"`
	Action      string   `json:"action"`
	ChallengeTS string   `json:"challenge_ts"`
	Hostname    string   `json:"hostname"`
	ErrorCodes  []string `json:"error-codes"`
}

// recaptchaSecretKey reads RECAPTCHA_SECRET_KEY from the environment at call
// time (so it is picked up after config.Load() runs).
func recaptchaSecretKey() string {
	return os.Getenv("RECAPTCHA_SECRET_KEY")
}

// callRecaptchaAPI calls Google's siteverify API and returns the response.
func callRecaptchaAPI(token, secret, remoteIP string) (*recaptchaResponse, error) {
	resp, err := http.PostForm(recaptchaVerifyURL, url.Values{
		"secret":   {secret},
		"response": {token},
		"remoteip": {remoteIP},
	})
	if err != nil {
		return nil, fmt.Errorf("recaptcha: http request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("recaptcha: failed to read response: %w", err)
	}

	var result recaptchaResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("recaptcha: failed to parse response: %w", err)
	}
	return &result, nil
}

// VerifyRecaptcha returns a Gin middleware that checks the reCAPTCHA v3 token
// present in the request body field "recaptcha_token".
//
// minScore is the minimum acceptable score (0.0 = bot, 1.0 = human).
// Recommended values: 0.5 for normal forms, 0.7 for high-risk operations.
//
// If RECAPTCHA_SECRET_KEY is not set (dev mode), the check is skipped so
// local development remains frictionless.
func VerifyRecaptcha(minScore float64) gin.HandlerFunc {
	return func(c *gin.Context) {
		secret := recaptchaSecretKey()

		// Dev/test: skip when secret is not configured.
		if secret == "" {
			log.Printf("[recaptcha] RECAPTCHA_SECRET_KEY not set — skipping check (dev mode) for %s", c.Request.URL.Path)
			c.Next()
			return
		}

		// Read the full body once, then restore it for the downstream handler.
		rawBody, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Pedido inválido."})
			c.Abort()
			return
		}
		// Restore body so the actual handler can still decode the full payload.
		c.Request.Body = io.NopCloser(bytes.NewReader(rawBody))

		// Extract only the recaptcha_token field from the body.
		var payload struct {
			RecaptchaToken string `json:"recaptcha_token"`
		}
		_ = json.Unmarshal(rawBody, &payload)

		if payload.RecaptchaToken == "" {
			log.Printf("[recaptcha] Missing token — IP=%s path=%s", clientIP(c), c.Request.URL.Path)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Verificação de segurança em falta. Por favor recarrega a página e tenta novamente."})
			c.Abort()
			return
		}

		result, err := callRecaptchaAPI(payload.RecaptchaToken, secret, clientIP(c))
		if err != nil {
			// Fail open on transient Google API errors — log but don't block legit users.
			log.Printf("[recaptcha] API error (fail-open): %v", err)
			c.Next()
			return
		}

		if !result.Success {
			log.Printf("[recaptcha] Invalid token — IP=%s errors=%v", clientIP(c), result.ErrorCodes)
			c.JSON(http.StatusForbidden, gin.H{"error": "Verificação de segurança falhou. Tenta novamente."})
			c.Abort()
			return
		}

		if result.Score < minScore {
			log.Printf("[recaptcha] Low score %.2f (min %.2f) — IP=%s action=%s",
				result.Score, minScore, clientIP(c), result.Action)
			c.JSON(http.StatusForbidden, gin.H{
				"error": "O teu pedido foi bloqueado por suspeita de atividade automatizada. Se és humano, tenta novamente.",
			})
			c.Abort()
			return
		}

		log.Printf("[recaptcha] OK — score=%.2f action=%s IP=%s", result.Score, result.Action, clientIP(c))
		c.Next()
	}
}
