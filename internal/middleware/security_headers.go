package middleware

import (
	"github.com/gin-gonic/gin"
)

// SecurityHeaders injects hardened HTTP security headers on every response.
//
// Headers applied:
//   - Strict-Transport-Security  — forces HTTPS for 1 year (HSTS)
//   - X-Content-Type-Options     — prevents MIME-type sniffing
//   - X-Frame-Options            — blocks clickjacking via iframes
//   - X-XSS-Protection           — enables legacy browser XSS filter
//   - Referrer-Policy            — limits referrer leakage
//   - Permissions-Policy         — disables unneeded browser APIs
//   - Content-Security-Policy    — restricts resource origins (backend-only; frontend has its own)
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Enforce HTTPS for 1 year; include sub-domains.
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")

		// Prevent MIME-type sniffing.
		c.Header("X-Content-Type-Options", "nosniff")

		// Deny rendering in any iframe/frame (anti-clickjacking).
		c.Header("X-Frame-Options", "DENY")

		// Legacy XSS filter (still honoured by some proxies/WAFs).
		c.Header("X-XSS-Protection", "1; mode=block")

		// Only send the origin when navigating to the same origin.
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")

		// Disable APIs that the API server never uses.
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")

		// Minimal CSP for the API server: no rendering, no scripts.
		// Frontend has its own, more permissive CSP in index.html.
		c.Header("Content-Security-Policy",
			"default-src 'none'; frame-ancestors 'none'; form-action 'none';")

		c.Next()
	}
}
