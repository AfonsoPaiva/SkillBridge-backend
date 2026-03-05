package middleware

import (
	"log"
	"sync"
	"time"
)

// --------------------------------------------------
// TOTP SESSION MANAGEMENT
// --------------------------------------------------

// TOTPSession represents an authenticated TOTP session
type TOTPSession struct {
	Expiry    time.Time
	CreatedAt time.Time
	LastUsed  time.Time
	IP        string
	UserAgent string
}

// totpSessionStore stores verified TOTP sessions
var totpSessionStore = make(map[string]*TOTPSession)
var totpSessionLock sync.RWMutex

// ValidateTOTPSession checks if a Firebase UID has a valid TOTP session
func ValidateTOTPSession(firebaseUID string) bool {
	totpSessionLock.RLock()
	session, exists := totpSessionStore[firebaseUID]
	totpSessionLock.RUnlock()

	if !exists {
		log.Printf("[TOTP Session] No session found for UID=%s", firebaseUID)
		return false
	}

	// Check if session is still valid
	if time.Now().Before(session.Expiry) {
		timeLeft := time.Until(session.Expiry).Round(time.Minute)
		log.Printf("[TOTP Session] Valid session for UID=%s (expires in %s)", firebaseUID, timeLeft)
		// Update last used time (async to not block)
		go func() {
			totpSessionLock.Lock()
			defer totpSessionLock.Unlock()
			if s, ok := totpSessionStore[firebaseUID]; ok {
				s.LastUsed = time.Now()
			}
		}()
		return true
	}

	// Session expired - acquire write lock to remove it
	log.Printf("[TOTP Session] Session expired for UID=%s (expired %s ago)", firebaseUID, time.Since(session.Expiry).Round(time.Minute))
	totpSessionLock.Lock()
	defer totpSessionLock.Unlock()
	// Double-check it's still expired (another goroutine might have refreshed it)
	if s, ok := totpSessionStore[firebaseUID]; ok && time.Now().After(s.Expiry) {
		delete(totpSessionStore, firebaseUID)
	}
	return false
}

// CreateTOTPSession creates a TOTP session that lasts for 8 hours
func CreateTOTPSession(firebaseUID string, ip string, userAgent string) {
	totpSessionLock.Lock()
	defer totpSessionLock.Unlock()

	session := &TOTPSession{
		Expiry:    time.Now().Add(8 * time.Hour),
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
		IP:        ip,
		UserAgent: userAgent,
	}
	totpSessionStore[firebaseUID] = session
	log.Printf("[TOTP Session] Created session for UID=%s IP=%s (expires at %s)", 
		firebaseUID, ip, session.Expiry.Format("15:04:05"))
}

// ClearTOTPSession removes a TOTP session (for logout)
func ClearTOTPSession(firebaseUID string) {
	totpSessionLock.Lock()
	defer totpSessionLock.Unlock()
	delete(totpSessionStore, firebaseUID)
	log.Printf("[TOTP Session] Cleared session for UID=%s", firebaseUID)
}

// GetSessionInfo retrieves session information for a user
func GetSessionInfo(firebaseUID string) *TOTPSession {
	totpSessionLock.RLock()
	defer totpSessionLock.RUnlock()
	return totpSessionStore[firebaseUID]
}
