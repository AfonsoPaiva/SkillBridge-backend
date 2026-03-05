package middleware

import (
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
	defer totpSessionLock.RUnlock()

	if session, exists := totpSessionStore[firebaseUID]; exists {
		if time.Now().Before(session.Expiry) {
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
		// Session expired, remove it
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
}

// ClearTOTPSession removes a TOTP session (for logout)
func ClearTOTPSession(firebaseUID string) {
	totpSessionLock.Lock()
	defer totpSessionLock.Unlock()
	delete(totpSessionStore, firebaseUID)
}

// GetSessionInfo retrieves session information for a user
func GetSessionInfo(firebaseUID string) *TOTPSession {
	totpSessionLock.RLock()
	defer totpSessionLock.RUnlock()
	return totpSessionStore[firebaseUID]
}
