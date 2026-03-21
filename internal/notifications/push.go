package notifications

import (
	"context"
	"log"
	"os"
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
	if messagingClient == nil || len(tokens) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	msg := &messaging.MulticastMessage{
		Tokens: tokens,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Data: data,
		Webpush: &messaging.WebpushConfig{
			Notification: &messaging.WebpushNotification{
				Title: title,
				Body:  body,
				Icon:  "/assets/favicon-192.png",
				Badge: "/assets/favicon-192.png",
			},
			FCMOptions: &messaging.WebpushFCMOptions{
				Link: "/messages",
			},
		},
	}

	_, err := messagingClient.SendEachForMulticast(ctx, msg)
	return err
}
