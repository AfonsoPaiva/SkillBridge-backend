package notifications

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"github.com/paiva/SkillBridge/Backend/config"
	"google.golang.org/api/option"
)

var messagingClient *messaging.Client

// InitFirebaseMessaging initializes Firebase Cloud Messaging client.
// Failure is non-fatal so the API can still run without push notifications.
func InitFirebaseMessaging() error {
	var opt option.ClientOption

	credsContent := os.Getenv("FIREBASE_CREDENTIALS_CONTENT")
	if credsContent != "" {
		opt = option.WithCredentialsJSON([]byte(credsContent))
	} else {
		opt = option.WithCredentialsFile(config.AppConfig.FirebaseCredentialsPath)
	}

	app, err := firebase.NewApp(context.Background(), nil, opt)
	if err != nil {
		return err
	}

	client, err := app.Messaging(context.Background())
	if err != nil {
		return err
	}

	messagingClient = client
	log.Println("Firebase Cloud Messaging inicializado com sucesso.")
	return nil
}

// SendMessagePush sends a push notification to one or more device tokens.
func SendMessagePush(tokens []string, title string, body string, data map[string]string) error {
	if messagingClient == nil {
		return fmt.Errorf("fcm messaging client not initialized")
	}

	if len(tokens) == 0 {
		return fmt.Errorf("no push tokens provided")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	frontendURL := strings.TrimSpace(config.AppConfig.FrontendURL)
	frontendURL = strings.TrimRight(frontendURL, "/")
	if frontendURL == "" {
		frontendURL = "https://skillbridge.pt"
	}

	messageLink := frontendURL + "/messages"

	payloadData := make(map[string]string, len(data)+1)
	for k, v := range data {
		payloadData[k] = v
	}

	if conversationID := strings.TrimSpace(payloadData["conversation_id"]); conversationID != "" {
		messageLink = frontendURL + "/messages/" + conversationID
	}

	payloadData["url"] = messageLink

	msg := &messaging.MulticastMessage{
		Tokens: tokens,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Data: payloadData,
		Webpush: &messaging.WebpushConfig{
			Notification: &messaging.WebpushNotification{
				Title: title,
				Body:  body,
				Icon:  "/assets/favicon-192.png",
				Badge: "/assets/favicon-192.png",
			},
			FCMOptions: &messaging.WebpushFCMOptions{
				Link: messageLink,
			},
		},
	}

	resp, err := messagingClient.SendEachForMulticast(ctx, msg)
	if err != nil {
		return err
	}

	if resp == nil {
		return fmt.Errorf("fcm returned empty response")
	}

	if resp.FailureCount > 0 {
		for i, r := range resp.Responses {
			if r != nil && r.Error != nil {
				log.Printf("[push] token[%d] failed: %v", i, r.Error)
			}
		}
	}

	if resp.SuccessCount == 0 {
		return fmt.Errorf("push delivery failed for all tokens (%d failures)", resp.FailureCount)
	}

	log.Printf("[push] delivery ok: success=%d failure=%d", resp.SuccessCount, resp.FailureCount)

	return nil
}
