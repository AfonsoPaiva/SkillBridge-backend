package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Request body size limiter — anti-DDoS / anti-memory-exhaustion
// ---------------------------------------------------------------------------
//
// Large request bodies can exhaust server memory and act as a primitive DDoS
// vector (e.g. POST 100 MB of JSON to /api/recruiters/apply repeatedly).
//
// Limits:
//   - DefaultBodyLimit: 512 KB — suitable for most JSON API endpoints.
//   - UploadBodyLimit:  10 MB  — for image upload endpoints.
//
// Usage:
//
//	api.Use(middleware.LimitBodySize(middleware.DefaultBodyLimit))
//	protected.POST("/upload/image", middleware.LimitBodySize(middleware.UploadBodyLimit), handler)
//
// ---------------------------------------------------------------------------

const (
	// DefaultBodyLimit caps general API payloads at 512 KB.
	DefaultBodyLimit int64 = 512 * 1024 // 512 KB

	// UploadBodyLimit caps image upload payloads at 10 MB.
	UploadBodyLimit int64 = 10 * 1024 * 1024 // 10 MB
)

// LimitBodySize returns a middleware that rejects requests whose Content-Length
// exceeds maxBytes, or whose streamed body exceeds maxBytes (using http.MaxBytesReader).
func LimitBodySize(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Reject immediately when Content-Length is provided and already too large.
		if c.Request.ContentLength > maxBytes {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": "O corpo do pedido é demasiado grande.",
			})
			c.Abort()
			return
		}

		// Wrap the body reader so that reading beyond the limit also fails.
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)

		c.Next()
	}
}

// ---------------------------------------------------------------------------
// SQL-injection / dangerous-input sanitizer — defence-in-depth
// ---------------------------------------------------------------------------
//
// GORM already uses parameterized queries, so classic SQL injection is blocked
// at the ORM level.  This middleware adds a second layer of defence that
// rejects requests whose query-string parameters or path contain suspicious
// patterns commonly used in injection probes, before any handler logic runs.
//
// It is intentionally conservative: only block patterns that have no
// legitimate use in this API's query strings (e.g. UNION SELECT, --comment,
// script tags, null bytes).  Blocked requests receive a 400 Bad Request so
// the attacker gets no useful information.
//
// NOTE: This does NOT replace parameterized queries; it is purely additive.
// ---------------------------------------------------------------------------

// suspiciousPatterns is a list of lower-cased substrings that indicate a probe.
var suspiciousPatterns = []string{
	// SQL keywords / operators used in injection
	"union select",
	"union all select",
	"1=1",
	"1 = 1",
	"' or '",
	"\" or \"",
	" or 1=1",
	" or 1 = 1",
	"'; drop",
	"\"; drop",
	"--",        // SQL single-line comment
	"/*",        // SQL block comment start
	"xp_cmdshell",
	"information_schema",
	"sleep(",
	"benchmark(",
	"load_file(",
	"outfile",
	"char(",      // CHAR() encoding bypass
	"concat(",
	"0x",         // hex-encoded bypass
	// Script/XSS payloads in URLs
	"<script",
	"javascript:",
	"onerror=",
	"onload=",
	// Path traversal
	"../",
	"..\\",
	// Null byte
	"\x00",
}

// SQLInjectionProtection inspects query parameters and the URL path for
// suspicious patterns.  It blocks the request with 400 Bad Request if any
// pattern is matched.  POST body inspection is left to the ORM layer.
func SQLInjectionProtection() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check URL path
		pathLower := strings.ToLower(c.Request.URL.RawPath)
		if pathLower == "" {
			pathLower = strings.ToLower(c.Request.URL.Path)
		}

		for _, pattern := range suspiciousPatterns {
			if strings.Contains(pathLower, pattern) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Pedido inválido."})
				c.Abort()
				return
			}
		}

		// Check all query parameters
		for key, values := range c.Request.URL.Query() {
			keyLower := strings.ToLower(key)
			for _, pattern := range suspiciousPatterns {
				if strings.Contains(keyLower, pattern) {
					c.JSON(http.StatusBadRequest, gin.H{"error": "Pedido inválido."})
					c.Abort()
					return
				}
			}
			for _, val := range values {
				valLower := strings.ToLower(val)
				for _, pattern := range suspiciousPatterns {
					if strings.Contains(valLower, pattern) {
						c.JSON(http.StatusBadRequest, gin.H{"error": "Pedido inválido."})
						c.Abort()
						return
					}
				}
			}
		}

		c.Next()
	}
}

// ---------------------------------------------------------------------------
// User-Agent bot filter — lightweight bot detection without reCAPTCHA
// ---------------------------------------------------------------------------
//
// Many automated scanners and scrapers advertise themselves via well-known
// User-Agent strings.  Blocking them here reduces noise on rate-limit buckets
// and handler logic.  This is NOT a substitute for reCAPTCHA on write
// endpoints — it just cuts down crawlers hitting read endpoints.
//
// Legitimate search-engine bots (Googlebot, Bingbot, etc.) are deliberately
// NOT blocked because they improve SEO.
// ---------------------------------------------------------------------------

// blockedAgentPatterns matches common scanner / exploit tool UAs (lower-case).
var blockedAgentPatterns = []string{
	"sqlmap",
	"nikto",
	"nmap",
	"masscan",
	"zgrab",
	"nuclei",
	"dirbuster",
	"gobuster",
	"wfuzz",
	"hydra",
	"acunetix",
	"nessus",
	"openvas",
	"metasploit",
	"havij",
	"python-requests/2.2",  // very old requests version used by many scripts
	"curl/7.3",
	"curl/7.4",
	"curl/7.5",
	"curl/7.6",
	"curl/7.7",
	"curl/7.8",
	"curl/7.9",
	"curl/7.10",
	"curl/7.11",
	"curl/7.12",
}

// BotProtection blocks requests from known scanner/exploit tool User-Agents.
// It returns 403 Forbidden with no body to give the scanner minimum information.
func BotProtection() gin.HandlerFunc {
	return func(c *gin.Context) {
		ua := strings.ToLower(c.GetHeader("User-Agent"))

		// Block empty User-Agent on write endpoints
		if ua == "" && isWriteMethod(c.Request.Method) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}

		for _, pattern := range blockedAgentPatterns {
			if strings.Contains(ua, pattern) {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
		}

		c.Next()
	}
}

// isWriteMethod returns true for HTTP methods that mutate state.
func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
