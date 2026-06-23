package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Generic sliding-window rate limiter
// ---------------------------------------------------------------------------
//
// Each limiter is keyed by client IP and tracks timestamps of recent requests
// within a rolling window. It is safe for concurrent use.
//
// Usage in routes.go:
//
//	r.Use(middleware.GlobalRateLimit())          // ~300 req/min per IP globally
//	api.POST("/endpoint", middleware.RateLimit(5, time.Minute), handler)
//
// ---------------------------------------------------------------------------

// rateBucket holds the sliding-window timestamps for a single IP.
type rateBucket struct {
	mu   sync.Mutex
	hits []time.Time
}

// rateLimiter maps client IPs → their request bucket.
type rateLimiter struct {
	mu      sync.RWMutex
	buckets map[string]*rateBucket
	max     int
	window  time.Duration
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	rl := &rateLimiter{
		buckets: make(map[string]*rateBucket),
		max:     max,
		window:  window,
	}
	// Periodically clean up stale entries to avoid memory leaks.
	go rl.cleanup()
	return rl
}

// allow returns true when the IP is within its quota, false when rate-limited.
func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.RLock()
	b, exists := rl.buckets[ip]
	rl.mu.RUnlock()

	if !exists {
		rl.mu.Lock()
		// Double-check after acquiring the write lock.
		b, exists = rl.buckets[ip]
		if !exists {
			b = &rateBucket{}
			rl.buckets[ip] = b
		}
		rl.mu.Unlock()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Evict timestamps older than the window.
	valid := b.hits[:0]
	for _, t := range b.hits {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= rl.max {
		b.hits = valid
		return false
	}

	b.hits = append(valid, now)
	return true
}

// cleanup removes IPs that have had no traffic for more than 2× the window.
func (rl *rateLimiter) cleanup() {
	tick := time.NewTicker(rl.window * 2)
	defer tick.Stop()
	for range tick.C {
		cutoff := time.Now().Add(-rl.window * 2)
		rl.mu.Lock()
		for ip, b := range rl.buckets {
			b.mu.Lock()
			if len(b.hits) == 0 || b.hits[len(b.hits)-1].Before(cutoff) {
				delete(rl.buckets, ip)
			}
			b.mu.Unlock()
		}
		rl.mu.Unlock()
	}
}

// ---------------------------------------------------------------------------
// Pre-configured limiters
// ---------------------------------------------------------------------------

var (
	// globalLimiter: broad safety net — 300 req/min per IP on all routes.
	globalLimiter = newRateLimiter(300, time.Minute)

	// sensitiveWriteLimiter: 20 req/min — public POST endpoints that touch
	// the database (guest sessions, recruiter apply, password-reset, etc.).
	sensitiveWriteLimiter = newRateLimiter(20, time.Minute)

	// authFlowLimiter: 10 req/min — recruiter request-link / verify-token.
	authFlowLimiter = newRateLimiter(10, time.Minute)

	// proxyImageLimiter: 120 req/min — image proxy (cache-miss path).
	proxyImageLimiter = newRateLimiter(120, time.Minute)

	// heavyReadLimiter: 60 req/min — vacancies list / projects list (heavy DB scans).
	heavyReadLimiter = newRateLimiter(60, time.Minute)
)

// ---------------------------------------------------------------------------
// Gin middleware factories
// ---------------------------------------------------------------------------

// clientIP resolves the real client IP, respecting X-Forwarded-For / X-Real-IP.
func clientIP(c *gin.Context) string {
	if xff := c.GetHeader("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For may contain a chain: "clientIP, proxy1, proxy2"
		// The leftmost IP is the original client.
		for i, ch := range xff {
			if ch == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	if xri := c.GetHeader("X-Real-IP"); xri != "" {
		return xri
	}
	return c.ClientIP()
}

func rateLimitMiddleware(rl *rateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := clientIP(c)
		if !rl.allow(ip) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "Demasiados pedidos. Por favor, tente novamente mais tarde.",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// Exported middleware functions (used in routes.go)
// ---------------------------------------------------------------------------

// GlobalRateLimit applies a broad 300 req/min per-IP cap to every route.
// Attach this early in the middleware chain with r.Use(…).
func GlobalRateLimit() gin.HandlerFunc {
	return rateLimitMiddleware(globalLimiter)
}

// SensitiveWriteRateLimit limits public write endpoints to 20 req/min per IP.
// Use on: POST /guest/session, POST /users/password-reset, POST /recruiters/apply.
func SensitiveWriteRateLimit() gin.HandlerFunc {
	return rateLimitMiddleware(sensitiveWriteLimiter)
}

// AuthFlowRateLimit limits recruiter auth flows to 10 req/min per IP.
// Use on: POST /recruiters/request-link, POST /recruiters/verify-token.
func AuthFlowRateLimit() gin.HandlerFunc {
	return rateLimitMiddleware(authFlowLimiter)
}

// ProxyImageRateLimit limits the image proxy to 120 req/min per IP.
// Cached responses are served before this middleware runs, so only cache-miss
// paths are effectively throttled.
func ProxyImageRateLimit() gin.HandlerFunc {
	return rateLimitMiddleware(proxyImageLimiter)
}

// HeavyReadRateLimit limits expensive list endpoints to 60 req/min per IP.
// Use on: GET /vacancies, GET /projects.
func HeavyReadRateLimit() gin.HandlerFunc {
	return rateLimitMiddleware(heavyReadLimiter)
}
