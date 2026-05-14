// Package email — Rate Limiter Inteligente de Emails
//
// Implementa dois mecanismos complementares de proteção contra spam de emails:
//
// 1. Limite diário global de emails enviados (protege o plano Resend de 100/dia).
// 2. Debouncing por (actor, alvo, tipo de notificação): agrupa eventos repetidos
//    dentro de uma janela configurável e envia apenas um email consolidado.
//    Exemplo: seguir → deixar de seguir → seguir novamente em <30 min → 1 email só.
package email

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────
// CONFIGURAÇÃO
// ─────────────────────────────────────────────────────────────

const (
	// DailyEmailLimit é o número máximo de emails que o sistema pode enviar por dia.
	// Mantido abaixo do limite do Resend (100/dia) para ter margem de segurança.
	DailyEmailLimit = 80

	// DebounceWindow é o período de tempo durante o qual eventos repetidos são
	// agrupados e apenas um email é enviado no final da janela.
	DebounceWindow = 30 * time.Minute

	// PerUserDailyLimit é o número máximo de emails que UM utilizador pode
	// provocar como actor (ex: seguir várias pessoas) por dia.
	PerUserDailyLimit = 10
)

// ─────────────────────────────────────────────────────────────
// CONTADORES DIÁRIOS
// ─────────────────────────────────────────────────────────────

// dailyCounter controla o total de emails enviados hoje.
type dailyCounter struct {
	mu      sync.Mutex
	count   int
	resetAt time.Time
}

var globalCounter = &dailyCounter{
	resetAt: startOfTomorrow(),
}

// perUserCounters controla quantos emails cada utilizador provocou hoje.
var (
	perUserMu       sync.Mutex
	perUserCounters = make(map[uint]*dailyCounter) // keyed by actor user ID
)

func startOfTomorrow() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
}

// canSendGlobal verifica se ainda há quota global disponível para hoje.
// Devolve false se o limite foi atingido.
func canSendGlobal() bool {
	globalCounter.mu.Lock()
	defer globalCounter.mu.Unlock()

	if time.Now().After(globalCounter.resetAt) {
		globalCounter.count = 0
		globalCounter.resetAt = startOfTomorrow()
	}
	if globalCounter.count >= DailyEmailLimit {
		log.Printf("[ratelimit] Limite diário global de %d emails atingido — email ignorado", DailyEmailLimit)
		return false
	}
	globalCounter.count++
	return true
}

// canSendForUser verifica se o utilizador actor ainda pode provocar emails hoje.
func canSendForUser(actorID uint) bool {
	if actorID == 0 {
		// Sistema/anónimo — não aplicar limite por utilizador
		return true
	}

	perUserMu.Lock()
	counter, ok := perUserCounters[actorID]
	if !ok {
		counter = &dailyCounter{resetAt: startOfTomorrow()}
		perUserCounters[actorID] = counter
	}
	perUserMu.Unlock()

	counter.mu.Lock()
	defer counter.mu.Unlock()

	if time.Now().After(counter.resetAt) {
		counter.count = 0
		counter.resetAt = startOfTomorrow()
	}
	if counter.count >= PerUserDailyLimit {
		log.Printf("[ratelimit] Utilizador %d atingiu o limite diário de %d emails — email ignorado", actorID, PerUserDailyLimit)
		return false
	}
	counter.count++
	return true
}

// ─────────────────────────────────────────────────────────────
// DEBOUNCING DE NOTIFICAÇÕES
// ─────────────────────────────────────────────────────────────

// debounceKey identifica univocamente uma notificação pendente.
// actorID = quem provocou a acção (pode ser 0 para o sistema)
// targetEmail = destinatário do email
// kind = tipo de notificação (ex: "follow", "project_match", etc.)
type debounceKey struct {
	actorID     uint
	targetEmail string
	kind        string
}

// pendingNotification representa uma notificação que está em espera
// para ser enviada após a janela de debounce.
type pendingNotification struct {
	timer    *time.Timer
	sendFunc func() // função que efetivamente envia o email
	// Permite substituir o conteúdo enquanto a janela ainda não expirou
	// (ex: se o utilizador voltar a seguir, actualiza o nome do seguidor)
	updateFunc func(newSend func())
}

var (
	debounceMu      sync.Mutex
	debounceMap     = make(map[debounceKey]*pendingNotification)
)

// scheduleDebounced agenda o envio de um email com debouncing.
//
// Se já existir uma notificação pendente para a mesma (actorID, targetEmail, kind):
//   - Cancela o timer anterior
//   - Substitui a função de envio pela nova (conteúdo atualizado)
//   - Reinicia o temporizador
//
// Caso contrário, agenda um novo envio após DebounceWindow.
//
// actorID: ID do utilizador que provocou a acção (0 = sistema)
// targetEmail: endereço do destinatário
// kind: tipo de notificação ("follow", "project_match", etc.)
// sendFunc: closure que executa o envio real do email
func scheduleDebounced(actorID uint, targetEmail, kind string, sendFunc func()) {
	key := debounceKey{actorID: actorID, targetEmail: targetEmail, kind: kind}

	debounceMu.Lock()
	existing, exists := debounceMap[key]
	if exists {
		// Já há uma notificação pendente — cancela o timer e reinicia
		existing.timer.Stop()
		existing.sendFunc = sendFunc
		existing.timer = time.AfterFunc(DebounceWindow, func() {
			debounceMu.Lock()
			entry, stillExists := debounceMap[key]
			if stillExists {
				fn := entry.sendFunc
				delete(debounceMap, key)
				debounceMu.Unlock()
				fn()
			} else {
				debounceMu.Unlock()
			}
		})
		debounceMu.Unlock()
		log.Printf("[ratelimit] Notificação '%s' para %s reagendada (janela de debounce reiniciada)", kind, targetEmail)
		return
	}

	// Primeira notificação — agenda após a janela de debounce
	entry := &pendingNotification{
		sendFunc: sendFunc,
	}
	entry.timer = time.AfterFunc(DebounceWindow, func() {
		debounceMu.Lock()
		e, stillExists := debounceMap[key]
		if stillExists {
			fn := e.sendFunc
			delete(debounceMap, key)
			debounceMu.Unlock()
			fn()
		} else {
			debounceMu.Unlock()
		}
	})
	debounceMap[key] = entry
	debounceMu.Unlock()

	log.Printf("[ratelimit] Notificação '%s' para %s agendada em %s", kind, targetEmail, DebounceWindow)
}

// cancelDebounced cancela uma notificação pendente (ex: utilizador deixou de seguir
// antes da janela expirar — não faz sentido enviar o email de "novo seguidor").
// Devolve true se havia uma notificação que foi cancelada.
func cancelDebounced(actorID uint, targetEmail, kind string) bool {
	key := debounceKey{actorID: actorID, targetEmail: targetEmail, kind: kind}

	debounceMu.Lock()
	defer debounceMu.Unlock()

	entry, exists := debounceMap[key]
	if !exists {
		return false
	}

	entry.timer.Stop()
	delete(debounceMap, key)
	log.Printf("[ratelimit] Notificação '%s' para %s cancelada (utilizador desfez a acção)", kind, targetEmail)
	return true
}

// ─────────────────────────────────────────────────────────────
// FUNÇÃO PÚBLICA PRINCIPAL
// ─────────────────────────────────────────────────────────────

// sendRateLimited é um wrapper em torno de send() que aplica os dois limites:
// 1. Limite diário global
// 2. Limite diário por utilizador actor
//
// Deve ser chamado DENTRO das goroutines de envio, imediatamente antes de send().
// actorID = 0 para emails de sistema (candidatura aceite/rejeitada, etc.)
func sendRateLimited(actorID uint, from, to, subject, html string) error {
	if !canSendForUser(actorID) {
		return fmt.Errorf("limite diário por utilizador atingido")
	}
	if !canSendGlobal() {
		return fmt.Errorf("limite diário global atingido")
	}
	return send(from, to, subject, html)
}

// ─────────────────────────────────────────────────────────────
// STATUS (para debugging / monitorização)
// ─────────────────────────────────────────────────────────────

// RateLimitStatus devolve o estado atual dos contadores (para admin/debug).
func RateLimitStatus() map[string]interface{} {
	globalCounter.mu.Lock()
	globalSent := globalCounter.count
	globalReset := globalCounter.resetAt
	globalCounter.mu.Unlock()

	debounceMu.Lock()
	pendingCount := len(debounceMap)
	debounceMu.Unlock()

	return map[string]interface{}{
		"global_sent_today":   globalSent,
		"global_limit":        DailyEmailLimit,
		"global_resets_at":    globalReset,
		"pending_debounced":   pendingCount,
		"debounce_window_min": int(DebounceWindow.Minutes()),
	}
}

// ─────────────────────────────────────────────────────────────
// HELPERS PÚBLICOS PARA OS HANDLERS
// ─────────────────────────────────────────────────────────────

// ScheduleFollowEmail agenda o envio de um email de "novo seguidor" com debouncing.
// Se o utilizador voltar a seguir dentro da janela de 30 min, o timer reinicia
// e apenas um email é enviado.
func ScheduleFollowEmail(actorID uint, targetEmail, targetName, followerName, profileURL string) {
	sendFunc := func() {
		from := getEmailFrom()
		subject := fmt.Sprintf("%s começou a seguir-te no SkillBridge", followerName)
		html := fmt.Sprintf(followNotificationTemplate, targetName, followerName, profileURL, profileURL)
		if err := sendRateLimited(actorID, from, targetEmail, subject, html); err != nil {
			log.Printf("[email] Erro ao enviar email de follow para %s: %v", targetEmail, err)
		}
	}
	scheduleDebounced(actorID, targetEmail, "follow", sendFunc)
}

// CancelFollowEmail cancela um email de follow pendente (utilizado no unfollow).
// Devolve true se havia um email pendente que foi cancelado.
func CancelFollowEmail(actorID uint, targetEmail string) bool {
	return cancelDebounced(actorID, targetEmail, "follow")
}

// ScheduleProjectMatchEmail agenda o envio de um email de match de projeto com debouncing.
//
// Se o dono do projeto editar as vagas/skills várias vezes dentro da janela de 30 min,
// apenas um email é enviado por utilizador-alvo no fim da janela (com os dados mais recentes).
// Isso previne spam quando o dono altera as skills de um projeto repetidamente.
//
// A chave de debounce é (actorID, targetEmail, "project_match:<projectID>") para que
// edições ao mesmo projeto colapses em uma única notificação por destinatário.
func ScheduleProjectMatchEmail(actorID, projectID uint, userEmail, userName, projectTitle, projectDescription, skills, projectURL string) {
	kind := fmt.Sprintf("project_match:%d", projectID)

	sendFunc := func() {
		from := getEmailFrom()
		subject := "Encontramos o projeto perfeito para ti!"
		html := fmt.Sprintf(perfectProjectTemplate, userName, projectTitle, projectDescription, skills, projectURL, projectURL)
		if err := sendRateLimited(actorID, from, userEmail, subject, html); err != nil {
			log.Printf("[email] Email de match (projeto %d) para %s ignorado ou falhou: %v", projectID, userEmail, err)
		}
	}
	scheduleDebounced(actorID, userEmail, kind, sendFunc)
}

