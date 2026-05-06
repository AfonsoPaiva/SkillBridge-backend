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

func buildWebpushTag(payloadData map[string]string) string {
	// Tag estável para agrupar/substituir notificações.
	// Ex: "msg:new_message:conv_123:sender_45"
	typ := strings.TrimSpace(payloadData["type"])
	if typ == "" {
		typ = "notification"
	}

	conversationID := strings.TrimSpace(payloadData["conversation_id"])
	senderID := strings.TrimSpace(payloadData["sender_id"])

	return fmt.Sprintf("msg:%s:conv_%s:sender_%s", typ, conversationID, senderID)
}

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
				// Tag estável para evitar “acumular” notificações antigas com a mesma finalidade.
				// (Webpush usa Tag/CollapseKey para agrupar/substituir em vez de criar sempre uma nova.)
				Tag: buildWebpushTag(payloadData),
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

// SendMessagePushToTopic sends a push notification to all users subscribed to a topic.
// This allows sending to all users without requiring individual device tokens.
func SendMessagePushToTopic(topic string, title string, body string, data map[string]string) error {
	if messagingClient == nil {
		return fmt.Errorf("fcm messaging client not initialized")
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

	msg := &messaging.Message{
		Topic: topic,
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
				Tag:   buildWebpushTag(payloadData),
			},
			FCMOptions: &messaging.WebpushFCMOptions{
				Link: messageLink,
			},
		},
	}

	id, err := messagingClient.Send(ctx, msg)
	if err != nil {
		log.Printf("[push] failed to send to topic %s: %v", topic, err)
		return err
	}

	log.Printf("[push] sent to topic %s with ID: %s", topic, id)
	return nil
}

// SubscribeToTopic subscribes a device token to a Firebase Cloud Messaging topic.
// This allows sending messages to all devices subscribed to that topic.
func SubscribeToTopic(topic string, tokens []string) error {
	if messagingClient == nil {
		return fmt.Errorf("fcm messaging client not initialized")
	}

	if len(tokens) == 0 {
		return fmt.Errorf("no tokens provided")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	resp, err := messagingClient.SubscribeToTopic(ctx, tokens, topic)
	if err != nil {
		log.Printf("[push] failed to subscribe tokens to topic %s: %v", topic, err)
		return err
	}

	if resp.FailureCount > 0 {
		log.Printf("[push] topic subscription partial: success=%d failures=%d (topic=%s)", resp.SuccessCount, resp.FailureCount, topic)
	} else {
		log.Printf("[push] subscribed %d tokens to topic %s", resp.SuccessCount, topic)
	}

	return nil
}
