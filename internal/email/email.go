// Package email - Resend Email Service
//
// Serviço de email usando Resend para envio de notificações transacionais.
// Suporta múltiplos tipos de emails: bem-vindas, candidaturas, follows, reviews, etc.
package email

import (
	"fmt"
	"log"

	"github.com/paiva/SkillBridge/Backend/config"
	"github.com/resend/resend-go/v3"
)

var emailClient *resend.Client

// InitResend inicializa o cliente Resend com a API key do .env
func InitResend() error {
	apiKey := config.AppConfig.ResendAPIKey
	if apiKey == "" {
		log.Printf("[email] RESEND_API_KEY não configurada — emails desativados")
		return nil
	}

	emailClient = resend.NewClient(apiKey)
	log.Println("[email] Resend inicializado com sucesso")
	return nil
}

// send envia um email via Resend
// from: endereço "noreply@" ou similar (deve estar verificado em Resend)
// to: endereço do destinatário
// subject: assunto do email
// html: conteúdo HTML do email
func send(from, to, subject, html string) error {
	if emailClient == nil {
		log.Printf("[email] Resend não inicializado — email para %s ignorado", to)
		return nil
	}

	params := &resend.SendEmailRequest{
		From:    from,
		To:      []string{to},
		Subject: subject,
		Html:    html,
	}

	sent, err := emailClient.Emails.Send(params)
	if err != nil {
		log.Printf("[email] Erro ao enviar email para %s: %v", to, err)
		return err
	}

	log.Printf("[email] Email enviado com sucesso para %s (ID: %s)", to, sent.Id)
	return nil
}

// getEmailFrom retorna o endereço "from" configurado (fallback para noreply@resend.dev)
func getEmailFrom() string {
	if config.AppConfig.EmailFromAddress != "" {
		return config.AppConfig.EmailFromAddress
	}
	return "onboarding@resend.dev" // Fallback Resend default
}

// ─────────────────────────────────────────────────────────────
// EMAIL TEMPLATES
// ─────────────────────────────────────────────────────────────

// SendWelcome envia um email de boas-vindas após registo
func SendWelcome(name, email string, frontendURL string) error {
	subject := "Bem-vindo ao SkillBridge! 🎉"
	html := fmt.Sprintf(welcomeTemplate, name, frontendURL, frontendURL)
	return send(getEmailFrom(), email, subject, html)
}

// SendProjectApplication notifica o proprietário de um projeto sobre uma nova candidatura
func SendProjectApplication(projectOwnerEmail, projectOwnerName, projectTitle, applicantName string, projectURL string) error {
	subject := fmt.Sprintf("Nova candidatura para o teu projeto: %s", projectTitle)
	html := fmt.Sprintf(projectApplicationTemplate, projectOwnerName, projectTitle, applicantName, projectURL, projectURL)
	return send(getEmailFrom(), projectOwnerEmail, subject, html)
}

// SendMessagesThreshold notifica um utilizador quando recebe mais de 5 mensagens não lidas
func SendMessagesThreshold(userEmail, userName string, unreadCount int, conversationURL string) error {
	subject := fmt.Sprintf("Tens %d mensagens não lidas no SkillBridge 💬", unreadCount)
	html := fmt.Sprintf(messagesThresholdTemplate, userName, unreadCount, conversationURL, conversationURL)
	return send(getEmailFrom(), userEmail, subject, html)
}

// SendFollowNotification notifica um utilizador quando é seguido
func SendFollowNotification(userEmail, userName, followerName, profileURL string) error {
	subject := fmt.Sprintf("%s está a seguir-te no SkillBridge 👥", followerName)
	html := fmt.Sprintf(followNotificationTemplate, userName, followerName, profileURL, profileURL)
	return send(getEmailFrom(), userEmail, subject, html)
}

// SendReviewApproved notifica um utilizador quando a sua review foi aprovada
func SendReviewApproved(userEmail, userName, reviewedName string, rating int, profileURL string) error {
	subject := fmt.Sprintf("A tua avaliação foi aprovada! ⭐")
	html := fmt.Sprintf(reviewApprovedTemplate, userName, reviewedName, rating, profileURL, profileURL)
	return send(getEmailFrom(), userEmail, subject, html)
}

// SendProjectMatches notifica um utilizador sobre projetos disponíveis com skills compatíveis
func SendProjectMatches(userEmail, userName string, projects []ProjectMatch, projectsURL string) error {
	subject := "Projetos disponíveis com as tuas competências 🚀"

	projectsHTML := ""
	for i, proj := range projects {
		if i > 0 {
			projectsHTML += "<tr><td colspan='3' style='height:16px;'></td></tr>"
		}
		projectsHTML += fmt.Sprintf(`
		<tr>
			<td style="padding:16px;background:#f9fafb;border-radius:8px;border:1px solid #e5e7eb;">
				<h4 style="margin:0 0 8px;color:#111827;font-size:16px;font-weight:600;">%s</h4>
				<p style="margin:0 0 12px;color:#6b7280;font-size:14px;line-height:1.5;">%s</p>
				<p style="margin:0;color:#9ca3af;font-size:13px;">
					<strong>Skills:</strong> %s
				</p>
			</td>
		</tr>`, proj.Title, proj.Description, proj.Skills)
	}

	html := fmt.Sprintf(projectMatchesTemplate, userName, len(projects), projectsHTML, projectsURL, projectsURL)
	return send(getEmailFrom(), userEmail, subject, html)
}

// ProjectMatch representa um projeto disponível com skills compatíveis
type ProjectMatch struct {
	Title       string
	Description string
	Skills      string
	URL         string
}

// ─────────────────────────────────────────────────────────────
// HTML TEMPLATES
// ─────────────────────────────────────────────────────────────

const emailWrapper = `<!DOCTYPE html>
<html>
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="margin:0;padding:0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;background:#f3f4f6;">
	<table width="100%%" cellpadding="0" cellspacing="0" style="background:#f3f4f6;padding:40px 20px;">
		<tr>
			<td align="center">
				<table width="600" cellpadding="0" cellspacing="0" style="background:white;border-radius:12px;box-shadow:0 4px 6px rgba(0,0,0,0.1);overflow:hidden;">
					<!-- Header com Logo -->
					<tr>
						<td style="padding:32px 40px;text-align:center;background:linear-gradient(135deg, #68007A 0%%, #9333ea 100%%);">
							<h1 style="margin:0;color:white;font-size:28px;font-weight:700;letter-spacing:-0.5px;">SkillBridge</h1>
							<p style="margin:8px 0 0;color:rgba(255,255,255,0.8);font-size:13px;">Conecta Talento com Oportunidade</p>
						</td>
					</tr>
					<!-- Conteúdo -->
					<tr>
						<td style="padding:40px;">
							%%s
						</td>
					</tr>
					<!-- Footer -->
					<tr>
						<td style="padding:24px 40px;background:#f9fafb;border-top:1px solid #e5e7eb;">
							<p style="margin:0;color:#6b7280;font-size:13px;text-align:center;">
								SkillBridge — Plataforma de Colaboração<br>
								<span style="font-size:12px;color:#9ca3af;">Este é um email automático. Por favor, não respondas a este endereço.</span>
							</p>
						</td>
					</tr>
				</table>
			</td>
		</tr>
	</table>
</body>
</html>`

const welcomeTemplate = `<h2 style="margin:0 0 16px;color:#111827;font-size:24px;font-weight:600;">Bem-vindo ao SkillBridge! 🎉</h2>
<p style="margin:0 0 16px;color:#374151;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#374151;font-size:16px;line-height:1.6;">
	Obrigado por criares uma conta no SkillBridge! A nossa plataforma liga estudantes a projetos colaborativos reais.
</p>
<p style="margin:0 0 32px;color:#374151;font-size:16px;line-height:1.6;">
	Aqui podes:
</p>
<ul style="margin:0 0 32px;padding-left:24px;color:#374151;font-size:15px;line-height:1.8;">
	<li>Explorar projetos disponíveis</li>
	<li>Candidatar-te a vagas que se adequam às tuas competências</li>
	<li>Colaborar com outros estudantes</li>
	<li>Construir o teu portfólio profissional</li>
</ul>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:linear-gradient(135deg, #68007A 0%%, #9333ea 100%%);color:white;padding:14px 32px;border-radius:8px;text-decoration:none;font-weight:600;font-size:15px;box-shadow:0 4px 6px rgba(104,0,122,0.25);">
				Explorar Projetos
			</a>
		</td>
	</tr>
</table>
<p style="margin:32px 0 0;color:#6b7280;font-size:14px;line-height:1.6;">
	Se tiveres dúvidas, podes sempre visitar a página inicial em <a href="%s" style="color:#9333ea;text-decoration:none;">skillbridge.com</a>.
</p>`

const projectApplicationTemplate = `<h2 style="margin:0 0 16px;color:#111827;font-size:24px;font-weight:600;">Nova Candidatura 📬</h2>
<p style="margin:0 0 16px;color:#374151;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#374151;font-size:16px;line-height:1.6;">
	<strong>%s</strong> candidatou-se ao teu projeto <strong>"%s"</strong>!
</p>
<p style="margin:0 0 32px;color:#6b7280;font-size:15px;line-height:1.6;">
	Podes rever a candidatura e responder (aceitar ou rejeitar) diretamente na plataforma.
</p>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:linear-gradient(135deg, #68007A 0%%, #9333ea 100%%);color:white;padding:14px 32px;border-radius:8px;text-decoration:none;font-weight:600;font-size:15px;box-shadow:0 4px 6px rgba(104,0,122,0.25);">
				Ver Candidatura
			</a>
		</td>
	</tr>
</table>
<p style="margin:24px 0 0;color:#6b7280;font-size:13px;">
	Vai para a tua dashboard em <a href="%s" style="color:#9333ea;text-decoration:none;">skillbridge.com</a> para gerir candidaturas.
</p>`

const messagesThresholdTemplate = `<h2 style="margin:0 0 16px;color:#111827;font-size:24px;font-weight:600;">Mensagens Não Lidas 💬</h2>
<p style="margin:0 0 16px;color:#374151;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#374151;font-size:16px;line-height:1.6;">
	Tens <strong>%d mensagens não lidas</strong> nas tuas conversas. Vê o que as pessoas têm a dizer!
</p>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:linear-gradient(135deg, #68007A 0%%, #9333ea 100%%);color:white;padding:14px 32px;border-radius:8px;text-decoration:none;font-weight:600;font-size:15px;box-shadow:0 4px 6px rgba(104,0,122,0.25);">
				Ler Mensagens
			</a>
		</td>
	</tr>
</table>
<p style="margin:24px 0 0;color:#6b7280;font-size:13px;">
	Vai para mensagens em <a href="%s" style="color:#9333ea;text-decoration:none;">skillbridge.com</a>.
</p>`

const followNotificationTemplate = `<h2 style="margin:0 0 16px;color:#111827;font-size:24px;font-weight:600;">Novo Seguidor 👥</h2>
<p style="margin:0 0 16px;color:#374151;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#374151;font-size:16px;line-height:1.6;">
	<strong>%s</strong> está agora a seguir-te no SkillBridge!
</p>
<p style="margin:0 0 32px;color:#6b7280;font-size:15px;line-height:1.6;">
	Podes ver o seu perfil e conhecer melhor os seus projetos e interesses.
</p>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:linear-gradient(135deg, #68007A 0%%, #9333ea 100%%);color:white;padding:14px 32px;border-radius:8px;text-decoration:none;font-weight:600;font-size:15px;box-shadow:0 4px 6px rgba(104,0,122,0.25);">
				Ver Perfil
			</a>
		</td>
	</tr>
</table>
<p style="margin:24px 0 0;color:#6b7280;font-size:13px;">
	Vai para a comunidade em <a href="%s" style="color:#9333ea;text-decoration:none;">skillbridge.com</a>.
</p>`

const reviewApprovedTemplate = `<h2 style="margin:0 0 16px;color:#111827;font-size:24px;font-weight:600;">Avaliação Aprovada! ⭐</h2>
<p style="margin:0 0 16px;color:#374151;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#374151;font-size:16px;line-height:1.6;">
	A tua avaliação de <strong>%d estrela(s)</strong> para <strong>%s</strong> foi aprovada e está agora visível no seu perfil!
</p>
<p style="margin:0 0 32px;color:#6b7280;font-size:15px;line-height:1.6;">
	As avaliações autênticas ajudam toda a comunidade a conhecer melhor os talentos no SkillBridge.
</p>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:linear-gradient(135deg, #68007A 0%%, #9333ea 100%%);color:white;padding:14px 32px;border-radius:8px;text-decoration:none;font-weight:600;font-size:15px;box-shadow:0 4px 6px rgba(104,0,122,0.25);">
				Ver Perfil
			</a>
		</td>
	</tr>
</table>
<p style="margin:24px 0 0;color:#6b7280;font-size:13px;">
	Vai para <a href="%s" style="color:#9333ea;text-decoration:none;">skillbridge.com</a>.
</p>`

const projectMatchesTemplate = `<h2 style="margin:0 0 16px;color:#111827;font-size:24px;font-weight:600;">Projetos Disponíveis 🚀</h2>
<p style="margin:0 0 16px;color:#374151;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#374151;font-size:16px;line-height:1.6;">
	Encontrámos <strong>%d projeto(s)</strong> aberto(s) que correspondem às tuas competências!
</p>
<table width="100%%" cellpadding="0" cellspacing="0" style="margin:0 0 32px;width:100%%;">
	%s
</table>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:linear-gradient(135deg, #68007A 0%%, #9333ea 100%%);color:white;padding:14px 32px;border-radius:8px;text-decoration:none;font-weight:600;font-size:15px;box-shadow:0 4px 6px rgba(104,0,122,0.25);">
				Ver Todos os Projetos
			</a>
		</td>
	</tr>
</table>
<p style="margin:24px 0 0;color:#6b7280;font-size:13px;">
	Explora oportunidades em <a href="%s" style="color:#9333ea;text-decoration:none;">skillbridge.com</a>.
</p>`

// NotifyUsersWithMatchingSkills notifica utilizadores que têm skills compatíveis com um projeto
// Esta função deve ser chamada quando um novo projeto é criado ou ativado
// Função signature para ser chamada do handler com acesso ao database
// Exemplo: email.NotifyUsersWithMatchingSkills(project, projectSkills)
// Implementação: passar como callback ou integrar diretamente no handler
