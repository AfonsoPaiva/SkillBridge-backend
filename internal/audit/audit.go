package audit

import (
	"fmt"
	"log"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/models"
)

// ActionType represents the type of admin action
type ActionType string

const (
	ActionLogin          ActionType = "LOGIN"
	ActionLogout         ActionType = "LOGOUT"
	ActionTOTPSetup      ActionType = "TOTP_SETUP"
	ActionTOTPVerify     ActionType = "TOTP_VERIFY"
	ActionTOTPVerifyFailed ActionType = "TOTP_VERIFY_FAILED"
	ActionTOTPEnabled    ActionType = "TOTP_ENABLED"
	ActionTOTPDisable    ActionType = "TOTP_DISABLE"
	ActionUserDelete     ActionType = "USER_DELETE"
	ActionProjectDelete  ActionType = "PROJECT_DELETE"
	ActionReviewApprove  ActionType = "REVIEW_APPROVE"
	ActionReviewReject   ActionType = "REVIEW_REJECT"
	ActionReviewDelete   ActionType = "REVIEW_DELETE"
	ActionUnauthorized   ActionType = "UNAUTHORIZED_ACCESS"
	ActionRateLimited    ActionType = "RATE_LIMITED"
)

// Log logs an admin action to both console and database
func Log(c *gin.Context, action ActionType, details string) {
	firebaseUID := c.GetString("firebase_uid")
	if firebaseUID == "" {
		firebaseUID = "anonymous"
	}

	ip := getClientIP(c)
	userAgent := c.GetHeader("User-Agent")

	// Console log with timestamp
	timestamp := time.Now().UTC().Format(time.RFC3339)
	log.Printf("[AUDIT] [%s] UID=%s IP=%s Action=%s Details=%s UA=%s",
		timestamp, firebaseUID, ip, action, details, userAgent)

	// Database log (async to not block requests)
	go func() {
		auditLog := models.AuditLog{
			FirebaseUID: firebaseUID,
			Action:      string(action),
			Details:     details,
			IPAddress:   ip,
			UserAgent:   userAgent,
			Timestamp:   time.Now().UTC(),
		}
		
		// Best effort - don't block if DB is down
		if err := database.DB.Create(&auditLog).Error; err != nil {
			log.Printf("[AUDIT] Failed to save to DB: %v", err)
		}
	}()
}

// LogAction is a convenience function for logging with custom details
func LogAction(c *gin.Context, action ActionType, format string, args ...interface{}) {
	details := fmt.Sprintf(format, args...)
	Log(c, action, details)
}

// LogSuccess logs a successful admin action
func LogSuccess(c *gin.Context, action ActionType, resourceType string, resourceID interface{}) {
	details := fmt.Sprintf("Successfully %s %s ID=%v", action, resourceType, resourceID)
	Log(c, action, details)
}

// LogFailure logs a failed admin action
func LogFailure(c *gin.Context, action ActionType, reason string) {
	details := fmt.Sprintf("Failed: %s", reason)
	Log(c, action, details)
}

// LogUnauthorized logs an unauthorized access attempt
func LogUnauthorized(c *gin.Context, reason string, attemptedResource string) {
	details := fmt.Sprintf("Unauthorized: %s | Resource: %s", reason, attemptedResource)
	Log(c, ActionUnauthorized, details)
}

// getClientIP extracts the real client IP from the request
// Checks X-Forwarded-For and X-Real-IP headers (for proxies/load balancers)
func getClientIP(c *gin.Context) string {
	// Check X-Forwarded-For header (proxy/load balancer)
	xff := c.GetHeader("X-Forwarded-For")
	if xff != "" {
		// Take the first IP if multiple are present
		if idx := len(xff); idx > 0 {
			return xff[:idx]
		}
	}

	// Check X-Real-IP header
	xri := c.GetHeader("X-Real-IP")
	if xri != "" {
		return xri
	}

	// Fallback to direct client IP
	return c.ClientIP()
}

// GetRecentLogs retrieves recent audit logs for a specific user or all users
func GetRecentLogs(firebaseUID string, limit int) ([]models.AuditLog, error) {
	var logs []models.AuditLog
	query := database.DB.Order("timestamp DESC")
	
	if firebaseUID != "" {
		query = query.Where("firebase_uid = ?", firebaseUID)
	}
	
	if limit > 0 {
		query = query.Limit(limit)
	}
	
	err := query.Find(&logs).Error
	return logs, err
}

// GetLogsByAction retrieves logs filtered by action type
func GetLogsByAction(action ActionType, limit int) ([]models.AuditLog, error) {
	var logs []models.AuditLog
	query := database.DB.Where("action = ?", string(action)).Order("timestamp DESC")
	
	if limit > 0 {
		query = query.Limit(limit)
	}
	
	err := query.Find(&logs).Error
	return logs, err
}

// CleanOldLogs removes audit logs older than the specified duration
// Recommended to run this periodically via cron job
func CleanOldLogs(olderThan time.Duration) error {
	cutoff := time.Now().UTC().Add(-olderThan)
	result := database.DB.Where("timestamp < ?", cutoff).Delete(&models.AuditLog{})
	
	if result.Error != nil {
		return result.Error
	}
	
	log.Printf("[AUDIT] Cleaned %d old audit logs (older than %v)", result.RowsAffected, olderThan)
	return nil
}
