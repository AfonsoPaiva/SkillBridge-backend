package handlers

// E2E Encrypted Messaging
//
// Architecture summary
// ────────────────────
// The server is a "dumb pipe": it stores only base64 ciphertext and the sender's
// ephemeral X25519 public key. Neither the server nor the database admin can read
// message content.
//
// Client responsibility (Angular / mobile)
// ────────────────────────────────────────
//   1. On first login, generate an X25519 keypair.
//      • Store the private key in secure storage (IndexedDB / Keychain).
//      • POST the base64 public key to   PUT /api/messages/keys
//
//   2. To send a message to user B:
//      a. GET /api/messages/keys/:user_b_id  → recipient's public key (base64)
//      b. Generate a random X25519 ephemeral keypair.
//      c. ECDH(ephemeral_private, recipient_public) → rawSecret
//      d. HKDF-SHA256(rawSecret, salt="skillbridge-msg", len=32) → symmetricKey
//      e. Encrypt plaintext with AES-256-GCM (nonce prepended to ciphertext).
//      f. base64(nonce + ciphertext)  → encrypted_content
//      g. base64(ephemeral_public)    → ephemeral_key
//      h. POST /api/conversations/:id/messages  { encrypted_content, ephemeral_key }
//
//   3. To decrypt a received message:
//      a. ECDH(own_private_key, msg.ephemeral_key)  → rawSecret
//      b. HKDF same as step 2-d.
//      c. AES-256-GCM decrypt base64-decoded encrypted_content.

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/paiva/SkillBridge/Backend/internal/database"
	"github.com/paiva/SkillBridge/Backend/internal/email"
	"github.com/paiva/SkillBridge/Backend/internal/models"
	"github.com/paiva/SkillBridge/Backend/internal/notifications"
)

// ─────────────────────────────────────────────────────────────
// CONSTANTS AND RATE LIMITING
// ─────────────────────────────────────────────────────────────

const (
	MaxMessageLength          = 400  // Maximum plaintext message length
	MaxEncryptedContentLength = 2000 // Maximum encrypted content length (base64)
	MaxMessagesPerMinute      = 15   // Maximum messages per user per minute
)

// Rate limiter: tracks message counts per user
var (
	messageCounts = make(map[uint][]time.Time)
	countsMutex   sync.Mutex
)

// checkRateLimit verifies if user can send a message
func checkRateLimit(userID uint) bool {
	countsMutex.Lock()
	defer countsMutex.Unlock()

	now := time.Now()
	cutoff := now.Add(-1 * time.Minute)

	// Get user's message timestamps
	timestamps := messageCounts[userID]

	// Remove timestamps older than 1 minute
	validTimestamps := []time.Time{}
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			validTimestamps = append(validTimestamps, ts)
		}
	}

	// Check if limit exceeded
	if len(validTimestamps) >= MaxMessagesPerMinute {
		return false
	}

	// Add current timestamp
	validTimestamps = append(validTimestamps, now)
	messageCounts[userID] = validTimestamps

	return true
}

// ─────────────────────────────────────────────────────────────
// PUBLIC KEY MANAGEMENT
// ─────────────────────────────────────────────────────────────

// RegisterPublicKey - Regista ou atualiza a chave pública X25519 do utilizador
//
// @Summary      Registar chave pública
// @Description  Guarda a chave pública X25519 (base64) do utilizador autenticado. Necessária para que outros utilizadores possam enviar mensagens cifradas.
// @Tags         messages
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        input  body  object{public_key=string}  true  "Chave pública X25519 em base64"
// @Success      200  {object}  models.UserPublicKey
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /messages/keys [put]
func RegisterPublicKey(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var user models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&user).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var input struct {
		PublicKey string `json:"public_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var record models.UserPublicKey
	now := time.Now()
	if database.DB.Where("user_id = ?", user.ID).First(&record).Error != nil {
		// First registration
		record = models.UserPublicKey{
			UserID:    user.ID,
			PublicKey: input.PublicKey,
			Algorithm: "X25519",
			UpdatedAt: now,
		}
		database.DB.Create(&record)
	} else {
		// Update existing key
		database.DB.Model(&record).Updates(models.UserPublicKey{
			PublicKey: input.PublicKey,
			UpdatedAt: now,
		})
	}

	c.JSON(http.StatusOK, record)
}

// GetPublicKey - Devolve a chave pública de um utilizador
//
// @Summary      Obter chave pública
// @Description  Devolve a chave pública X25519 (base64) de um utilizador, necessária para cifrar mensagens destinadas a ele
// @Tags         messages
// @Produce      json
// @Param        user_id  path  int  true  "ID do utilizador destinatário"
// @Success      200  {object}  models.UserPublicKey
// @Failure      404  {object}  map[string]string
// @Router       /messages/keys/{user_id} [get]
func GetPublicKey(c *gin.Context) {
	var record models.UserPublicKey
	if err := database.DB.Where("user_id = ?", c.Param("user_id")).First(&record).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Chave pública não encontrada. O utilizador ainda não registou a sua chave."})
		return
	}
	// Omit the internal user relation — only expose the key itself
	c.JSON(http.StatusOK, gin.H{
		"user_id":    record.UserID,
		"public_key": record.PublicKey,
		"algorithm":  record.Algorithm,
		"updated_at": record.UpdatedAt,
	})
}

// ─────────────────────────────────────────────────────────────
// CONVERSATIONS
// ─────────────────────────────────────────────────────────────

// StartOrGetConversation - Cria ou devolve uma conversa entre dois utilizadores
//
// @Summary      Iniciar conversa
// @Description  Cria uma conversa privada com outro utilizador (idempotente — se já existir devolve a existente)
// @Tags         messages
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        input  body  object{user_id=integer}  true  "ID do outro utilizador"
// @Success      200  {object}  models.Conversation
// @Failure      400  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /conversations [post]
func StartOrGetConversation(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var me models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&me).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var input struct {
		UserID uint `json:"user_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if me.ID == input.UserID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Não pode iniciar uma conversa consigo mesmo."})
		return
	}

	// Verify the other user exists
	var other models.User
	if err := database.DB.First(&other, input.UserID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador destinatário não encontrado."})
		return
	}

	// Enforce userA < userB so the unique pair constraint works correctly
	userAID, userBID := me.ID, other.ID
	if userAID > userBID {
		userAID, userBID = userBID, userAID
	}

	var conv models.Conversation
	result := database.DB.Where("user_a_id = ? AND user_b_id = ?", userAID, userBID).First(&conv)
	if result.Error != nil {
		conv = models.Conversation{UserAID: userAID, UserBID: userBID}
		database.DB.Create(&conv)
	}

	database.DB.Preload("UserA").Preload("UserB").First(&conv, conv.ID)
	// Strip emails from public profiles
	conv.UserA.Email = ""
	conv.UserB.Email = ""

	c.JSON(http.StatusOK, conv)
}

// ListConversations - Lista as conversas do utilizador autenticado
//
// @Summary      Listar conversas
// @Description  Devolve todas as conversas do utilizador autenticado, ordenadas pela mais recente
// @Tags         messages
// @Produce      json
// @Security     BearerAuth
// @Success      200  {array}   models.Conversation
// @Failure      404  {object}  map[string]string
// @Router       /conversations [get]
func ListConversations(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var me models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&me).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var convs []models.Conversation
	database.DB.
		Preload("UserA").
		Preload("UserB").
		Where("user_a_id = ? OR user_b_id = ?", me.ID, me.ID).
		Order("created_at DESC").
		Find(&convs)

	// Add unread count for each conversation
	type ConversationWithUnread struct {
		models.Conversation
		UnreadCount int `json:"unread_count"`
	}

	result := make([]ConversationWithUnread, len(convs))
	for i := range convs {
		// Strip emails from public profiles
		convs[i].UserA.Email = ""
		convs[i].UserB.Email = ""

		// Count unread messages (messages sent by the other user that haven't been read)
		var unreadCount int64
		database.DB.Model(&models.Message{}).
			Where("conversation_id = ? AND sender_id != ? AND read_at IS NULL", convs[i].ID, me.ID).
			Count(&unreadCount)

		result[i] = ConversationWithUnread{
			Conversation: convs[i],
			UnreadCount:  int(unreadCount),
		}
	}

	c.JSON(http.StatusOK, result)
}

// ─────────────────────────────────────────────────────────────
// MESSAGES
// ─────────────────────────────────────────────────────────────

// SendMessage - Envia uma mensagem cifrada numa conversa
//
// @Summary      Enviar mensagem
// @Description  Envia uma mensagem E2E cifrada. O corpo deve conter o ciphertext em base64 (AES-256-GCM) e a chave efémera X25519 (base64) usada no ECDH.
// @Tags         messages
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id     path  int  true  "ID da conversa"
// @Param        input  body  object{encrypted_content=string,ephemeral_key=string}  true  "Conteúdo cifrado e chave efémera"
// @Success      201  {object}  models.Message
// @Failure      400  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /conversations/{id}/messages [post]
func SendMessage(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var me models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&me).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var conv models.Conversation
	if err := database.DB.First(&conv, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Conversa não encontrada."})
		return
	}

	// The authenticated user must be a participant
	if conv.UserAID != me.ID && conv.UserBID != me.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Não é participante desta conversa."})
		return
	}

	var input struct {
		EncryptedContent string `json:"encrypted_content" binding:"required"`
		EphemeralKey     string `json:"ephemeral_key" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Rate limiting: check if user exceeded message limit
	if !checkRateLimit(me.ID) {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error": fmt.Sprintf("Limite de mensagens excedido. Máximo: %d mensagens por minuto.", MaxMessagesPerMinute),
		})
		return
	}

	// Validate message length
	// For plain messages (ephemeral_key='plain'), decode and check plaintext length
	if input.EphemeralKey == "plain" {
		decoded, err := base64.StdEncoding.DecodeString(input.EncryptedContent)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Conteúdo inválido."})
			return
		}
		if len(decoded) > MaxMessageLength {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Mensagem demasiado longa. Máximo: %d caracteres.", MaxMessageLength),
			})
			return
		}
	} else {
		// For encrypted messages, check encrypted content length
		if len(input.EncryptedContent) > MaxEncryptedContentLength {
			c.JSON(http.StatusBadRequest, gin.H{
				"error": fmt.Sprintf("Conteúdo cifrado demasiado longo. Máximo: %d caracteres.", MaxEncryptedContentLength),
			})
			return
		}
	}

	msg := models.Message{
		ConversationID:   conv.ID,
		SenderID:         me.ID,
		EncryptedContent: input.EncryptedContent,
		EphemeralKey:     input.EphemeralKey,
	}
	database.DB.Create(&msg)

	// Push notification to the other conversation participant (best-effort)
	recipientID := conv.UserAID
	if recipientID == me.ID {
		recipientID = conv.UserBID
	}

	// Check if recipient now has 5+ unread messages (async, non-blocking)
	go func() {
		var unreadCount int64
		database.DB.Model(&models.Message{}).
			Where("sender_id = ? AND read_at IS NULL", recipientID).
			Count(&unreadCount)

		// If recipient has 5 or more unread messages, send an email notification
		if unreadCount >= 5 {
			var recipient models.User
			if err := database.DB.First(&recipient, recipientID).Error; err == nil {
				conversationURL := fmt.Sprintf("%s/messages", config.AppConfig.FrontendURL)
				if err := email.SendMessagesThreshold(recipient.Email, recipient.Name, int(unreadCount), conversationURL); err != nil {
					log.Printf("[email] Erro ao enviar email de threshold de mensagens para %s: %v", recipient.Email, err)
				}
			}
		}
	}()

	var recipientTokens []models.PushDeviceToken
	if err := database.DB.Where("user_id = ?", recipientID).Find(&recipientTokens).Error; err == nil {
		tokens := make([]string, 0, len(recipientTokens))
		for _, t := range recipientTokens {
			if t.Token != "" {
				tokens = append(tokens, t.Token)
			}
		}

		log.Printf("[push] conversation=%d recipient=%d tokens_found=%d", conv.ID, recipientID, len(tokens))

		if len(tokens) > 0 {
			notificationBody := "Recebeu uma nova mensagem no SkillBridge."
			if me.Name != "" {
				notificationBody = fmt.Sprintf("%s enviou-lhe uma nova mensagem.", me.Name)
			}

			if err := notifications.SendMessagePush(tokens, "Nova mensagem", notificationBody, map[string]string{
				"type":            "new_message",
				"conversation_id": fmt.Sprintf("%d", conv.ID),
				"sender_id":       fmt.Sprintf("%d", me.ID),
			}); err != nil {
				log.Printf("[push] failed to send message notification (conv=%d recipient=%d): %v", conv.ID, recipientID, err)
			}
		} else {
			log.Printf("[push] skipped send (conv=%d recipient=%d): no tokens", conv.ID, recipientID)
		}
	} else {
		log.Printf("[push] failed to query recipient tokens (conv=%d recipient=%d): %v", conv.ID, recipientID, err)
	}

	// Return sender info (no email)
	database.DB.Preload("Sender").First(&msg, msg.ID)
	msg.Sender.Email = ""

	c.JSON(http.StatusCreated, msg)
}

// GetMessages - Lista as mensagens cifradas de uma conversa
//
// @Summary      Listar mensagens
// @Description  Devolve as mensagens cifradas de uma conversa. O cliente desencripta-as localmente com a sua chave privada.
// @Tags         messages
// @Produce      json
// @Security     BearerAuth
// @Param        id     path   int  true  "ID da conversa"
// @Param        limit  query  int  false "Número máximo de mensagens (default 50)"
// @Param        before query  int  false "Devolver apenas mensagens com ID inferior a este (paginação)"
// @Success      200  {array}   models.Message
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /conversations/{id}/messages [get]
func GetMessages(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var me models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&me).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var conv models.Conversation
	if err := database.DB.First(&conv, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Conversa não encontrada."})
		return
	}

	if conv.UserAID != me.ID && conv.UserBID != me.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Não é participante desta conversa."})
		return
	}

	limit := 50
	query := database.DB.
		Preload("Sender").
		Where("conversation_id = ?", conv.ID).
		Order("created_at DESC").
		Limit(limit)

	if before := c.Query("before"); before != "" {
		query = query.Where("id < ?", before)
	}
	if l := c.Query("limit"); l != "" {
		var n int
		if _, err := fmt.Sscanf(l, "%d", &n); err == nil && n > 0 && n <= 100 {
			query = query.Limit(n)
		}
	}

	var messages []models.Message
	query.Find(&messages)

	// Strip email from sender profiles
	for i := range messages {
		messages[i].Sender.Email = ""
	}

	c.JSON(http.StatusOK, messages)
}

// MarkRead - Marca as mensagens de uma conversa como lidas pelo utilizador autenticado
//
// @Summary      Marcar como lido
// @Description  Atualiza o campo read_at das mensagens recebidas ainda não lidas
// @Tags         messages
// @Produce      json
// @Security     BearerAuth
// @Param        id  path  int  true  "ID da conversa"
// @Success      200  {object}  map[string]string
// @Failure      403  {object}  map[string]string
// @Failure      404  {object}  map[string]string
// @Router       /conversations/{id}/read [put]
func MarkRead(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var me models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&me).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var conv models.Conversation
	if err := database.DB.First(&conv, c.Param("id")).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Conversa não encontrada."})
		return
	}

	if conv.UserAID != me.ID && conv.UserBID != me.ID {
		c.JSON(http.StatusForbidden, gin.H{"error": "Não é participante desta conversa."})
		return
	}

	now := time.Now()
	database.DB.Model(&models.Message{}).
		Where("conversation_id = ? AND sender_id != ? AND read_at IS NULL", conv.ID, me.ID).
		Update("read_at", now)

	c.JSON(http.StatusOK, gin.H{"message": "Mensagens marcadas como lidas."})
}

// GetUnreadCount - Retorna o número total de mensagens não lidas
//
// @Summary      Contador de não lidas
// @Description  Devolve o número total de mensagens não lidas do utilizador
// @Tags         messages
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]int
// @Failure      404  {object}  map[string]string
// @Router       /messages/unread-count [get]
func GetUnreadCount(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var me models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&me).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	// Get all conversations where user participates
	var convIDs []uint
	database.DB.Model(&models.Conversation{}).
		Where("user_a_id = ? OR user_b_id = ?", me.ID, me.ID).
		Pluck("id", &convIDs)

	// Count unread messages across all conversations
	var unreadCount int64
	database.DB.Model(&models.Message{}).
		Where("conversation_id IN ? AND sender_id != ? AND read_at IS NULL", convIDs, me.ID).
		Count(&unreadCount)

	c.JSON(http.StatusOK, gin.H{"unread_count": unreadCount})
}

// RegisterPushToken stores or updates a push token for the authenticated user.
func RegisterPushToken(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var me models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&me).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var input struct {
		Token    string `json:"token" binding:"required"`
		Platform string `json:"platform"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	platform := input.Platform
	if platform != "android" && platform != "ios" {
		platform = "web"
	}

	now := time.Now()
	userAgent := c.GetHeader("User-Agent")

	var record models.PushDeviceToken
	err := database.DB.Where("token = ?", input.Token).First(&record).Error
	if err == nil {
		record.UserID = me.ID
		record.Platform = platform
		record.UserAgent = userAgent
		record.LastSeenAt = now
		database.DB.Save(&record)
		log.Printf("[push] token updated user=%d platform=%s", me.ID, platform)
		c.JSON(http.StatusOK, gin.H{"message": "Token atualizado com sucesso."})
		return
	}

	record = models.PushDeviceToken{
		UserID:     me.ID,
		Token:      input.Token,
		Platform:   platform,
		UserAgent:  userAgent,
		LastSeenAt: now,
	}
	database.DB.Create(&record)
	log.Printf("[push] token registered user=%d platform=%s", me.ID, platform)

	c.JSON(http.StatusCreated, gin.H{"message": "Token registado com sucesso."})
}

// DeletePushToken removes a push token from the authenticated user.
func DeletePushToken(c *gin.Context) {
	firebaseUID := c.GetString("firebase_uid")

	var me models.User
	if err := database.DB.Where("firebase_uid = ?", firebaseUID).First(&me).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Utilizador não encontrado."})
		return
	}

	var input struct {
		Token string `json:"token" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	database.DB.Where("user_id = ? AND token = ?", me.ID, input.Token).Delete(&models.PushDeviceToken{})
	c.JSON(http.StatusOK, gin.H{"message": "Token removido com sucesso."})
}
