// Package email - Resend Email Service
//
// Serviço de email usando Resend para envio de notificações transacionais.
// Suporta múltiplos tipos de emails: bem-vindas, candidaturas, follows, reviews, etc.
package email

import (
	"fmt"
	"log"
	"time"

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

// send envia um email via Resend com headers transacionais para garantir entrega na inbox principal
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
		Headers: map[string]string{
			// Sinaliza ao Gmail que é um email transacional individual (não marketing em massa)
			"X-Entity-Ref-ID": fmt.Sprintf("skillbridge-tx-%d", time.Now().UnixNano()),
			// Precedência de email normal
			"Precedence": "normal",
		},
	}

	sent, err := emailClient.Emails.Send(params)
	if err != nil {
		log.Printf("[email] Erro ao enviar email para %s: %v", to, err)
		return err
	}

	log.Printf("[email] Email enviado com sucesso para %s (ID: %s)", to, sent.Id)
	return nil
}

func getEmailFrom() string {
	address := "onboarding@resend.dev"
	if config.AppConfig.EmailFromAddress != "" {
		address = config.AppConfig.EmailFromAddress
	}
	return fmt.Sprintf("SkillBridge <%s>", address)
}

// ─────────────────────────────────────────────────────────────
// EMAIL TEMPLATES
// ─────────────────────────────────────────────────────────────

func SendWelcome(name, email string, frontendURL string) error {
	subject := "Bem-vindo ao SkillBridge"
	html := fmt.Sprintf(welcomeTemplate, name, frontendURL, frontendURL)
	return send(getEmailFrom(), email, subject, html)
}

func SendProjectApplication(projectOwnerEmail, projectOwnerName, projectTitle, applicantName string, messagesURL string) error {
	subject := fmt.Sprintf("Nova candidatura: %s", projectTitle)
	html := fmt.Sprintf(projectApplicationTemplate, projectOwnerName, applicantName, projectTitle, messagesURL)
	return send(getEmailFrom(), projectOwnerEmail, subject, html)
}

func SendMessagesThreshold(userEmail, userName string, unreadCount int, messagesURL string) error {
	subject := fmt.Sprintf("%d mensagens não lidas no SkillBridge", unreadCount)
	html := fmt.Sprintf(messagesThresholdTemplate, userName, unreadCount, messagesURL, messagesURL)
	return send(getEmailFrom(), userEmail, subject, html)
}

func SendFollowNotification(userEmail, userName, followerName, profileURL string) error {
	subject := fmt.Sprintf("%s começou a seguir-te no SkillBridge", followerName)
	html := fmt.Sprintf(followNotificationTemplate, userName, followerName, profileURL, profileURL)
	return send(getEmailFrom(), userEmail, subject, html)
}

func SendReviewApproved(userEmail, userName, reviewedName string, rating int, profileURL string) error {
	subject := "A tua avaliação foi aprovada"
	html := fmt.Sprintf(reviewApprovedTemplate, userName, rating, reviewedName, profileURL, profileURL)
	return send(getEmailFrom(), userEmail, subject, html)
}

func SendProjectMatches(userEmail, userName string, projects []ProjectMatch, projectsURL string) error {
	subject := fmt.Sprintf("%d projetos compatíveis com as tuas competências", len(projects))

	projectsHTML := ""
	for i, proj := range projects {
		if i > 0 {
			projectsHTML += "<tr><td colspan='3' style='height:16px;'></td></tr>"
		}
		projectsHTML += fmt.Sprintf(`
		<tr>
			<td style="padding:20px;background:#f8f9fa;border-radius:8px;border:1px solid #e9ecef;">
				<h4 style="margin:0 0 8px;color:#1a1a1a;font-size:16px;font-weight:600;">%s</h4>
				<p style="margin:0 0 12px;color:#4a4a4a;font-size:14px;line-height:1.5;">%s</p>
				<p style="margin:0;color:#6c757d;font-size:13px;">
					<strong>Skills:</strong> %s
				</p>
			</td>
		</tr>`, proj.Title, proj.Description, proj.Skills)
	}

	html := fmt.Sprintf(projectMatchesTemplate, userName, len(projects), projectsHTML, projectsURL)
	return send(getEmailFrom(), userEmail, subject, html)
}

func SendPerfectProjectMatch(userEmail, userName, projectTitle, projectDescription, skills, projectURL string) error {
	subject := "Encontramos o projeto perfeito para ti!"
	html := fmt.Sprintf(perfectProjectTemplate, userName, projectTitle, projectDescription, skills, projectURL, projectURL)
	return send(getEmailFrom(), userEmail, subject, html)
}

// SendCustomMarketing envia um email de marketing com HTML totalmente customizado pelo admin.
// - NÃO está sujeito ao rate limiting diário (global nem por utilizador).
// - O HTML é enviado diretamente sem qualquer template wrapper adicional.
func SendCustomMarketing(subject, toEmail, htmlBody string) error {
	if emailClient == nil {
		log.Printf("[email] Resend não inicializado — marketing email para %s ignorado", toEmail)
		return nil
	}

	params := &resend.SendEmailRequest{
		From:    getEmailFrom(),
		To:      []string{toEmail},
		Subject: subject,
		Html:    htmlBody,
	}

	sent, err := emailClient.Emails.Send(params)
	if err != nil {
		log.Printf("[email] Erro ao enviar marketing email para %s: %v", toEmail, err)
		return err
	}

	log.Printf("[email] Marketing email enviado para %s (ID: %s)", toEmail, sent.Id)
	return nil
}


// SendProjectDecisionApproved envia email ao candidato quando a candidatura é aprovada
func SendProjectDecisionApproved(userEmail, userName, projectTitle, projectURL, ownerName string) error {
	subject := fmt.Sprintf("A tua candidatura foi aprovada: %s", projectTitle)
	html := fmt.Sprintf(projectDecisionApprovedTemplate, userName, projectTitle, ownerName, projectURL, projectURL)
	return send(getEmailFrom(), userEmail, subject, html)
}

// SendProjectDecisionRejected envia email ao candidato quando a candidatura é rejeitada
func SendProjectDecisionRejected(userEmail, userName, projectTitle, projectURL, ownerName string) error {
	subject := fmt.Sprintf("A tua candidatura foi rejeitada: %s", projectTitle)
	html := fmt.Sprintf(projectDecisionRejectedTemplate, userName, projectTitle, ownerName, projectURL, projectURL)
	return send(getEmailFrom(), userEmail, subject, html)
}

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
	<link href="https://fonts.googleapis.com/css2?family=Plus+Jakarta+Sans:wght@400;500;600;700&display=swap" rel="stylesheet">
</head>
<body style="margin:0;padding:0;font-family:'Plus Jakarta Sans',-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;background:#f5f5f5;">
	<table width="100%%" cellpadding="0" cellspacing="0" style="background:#f5f5f5;padding:40px 20px;">
		<tr>
			<td align="center">
				<table width="600" cellpadding="0" cellspacing="0" style="background:#ffffff;border-radius:12px;box-shadow:0 2px 8px rgba(0,0,0,0.08);overflow:hidden;">
					<!-- Header com Logo -->
					<tr>
						<td style="padding:40px 40px 32px;text-align:center;background:#ffffff;border-bottom:3px solid #68007a;">
							<img src="https://storage.googleapis.com/skillbridge-uploads/Logo%%20Vertical.png" alt="SkillBridge" style="width:120px;height:auto;margin-bottom:16px;">
							<p style="margin:0;color:#4a4a4a;font-size:14px;font-weight:500;">Conecta Talento com Oportunidade</p>
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
						<td style="padding:24px 40px;background:#f8f9fa;border-top:1px solid #e9ecef;">
							<p style="margin:0;color:#6c757d;font-size:13px;text-align:center;line-height:1.6;">
								SkillBridge — Plataforma de Colaboração Profissional<br>
								<span style="font-size:12px;color:#999;">Este é um email automático. Por favor, não respondas a este endereço.</span>
							</p>
						</td>
					</tr>
				</table>
			</td>
		</tr>
	</table>
</body>
</html>`

const welcomeTemplate = `<h2 style="margin:0 0 20px;color:#1a1a1a;font-size:24px;font-weight:600;">Bem-vindo ao SkillBridge</h2>
<p style="margin:0 0 16px;color:#333;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#333;font-size:16px;line-height:1.6;">
	A tua conta foi criada com sucesso. O SkillBridge conecta estudantes e profissionais a projetos colaborativos reais, onde podes desenvolver as tuas competências e construir um portfólio sólido.
</p>
<p style="margin:0 0 32px;color:#333;font-size:16px;line-height:1.6;">
	<strong>Na plataforma podes:</strong>
</p>
<ul style="margin:0 0 32px;padding-left:24px;color:#333;font-size:15px;line-height:1.8;">
	<li>Explorar projetos disponíveis na tua área</li>
	<li>Candidatar-te a vagas alinhadas com as tuas competências</li>
	<li>Colaborar com outros profissionais e estudantes</li>
	<li>Construir o teu portfólio profissional</li>
</ul>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:#68007a;color:#ffffff;padding:16px 40px;border-radius:6px;text-decoration:none;font-weight:600;font-size:16px;text-align:center;">
				Explorar Projetos
			</a>
		</td>
	</tr>
</table>
<p style="margin:32px 0 0;color:#666;font-size:14px;line-height:1.6;">
	Se tiveres alguma dúvida, visita a nossa plataforma em <a href="%s" style="color:#68007a;text-decoration:none;">skillbridge.pt</a>.
</p>`

const projectApplicationTemplate = `<h2 style="margin:0 0 20px;color:#1a1a1a;font-size:24px;font-weight:600;">Nova Candidatura Recebida</h2>
<p style="margin:0 0 16px;color:#333;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#333;font-size:16px;line-height:1.6;">
	<strong>%s</strong> candidatou-se ao teu projeto <strong>"%s"</strong>.
</p>
<p style="margin:0 0 32px;color:#666;font-size:15px;line-height:1.6;">
	Analisa o perfil do candidato e responde à candidatura através das tuas mensagens na plataforma.
</p>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:#68007a;color:#ffffff;padding:16px 40px;border-radius:6px;text-decoration:none;font-weight:600;font-size:16px;text-align:center;">
				Ver Mensagens
			</a>
		</td>
	</tr>
</table>
<p style="margin:32px 0 0;color:#666;font-size:14px;line-height:1.6;">
	Acede às tuas <a href="%s" style="color:#68007a;text-decoration:none;">mensagens</a> para gerir todas as candidaturas.
</p>`

const messagesThresholdTemplate = `<h2 style="margin:0 0 20px;color:#1a1a1a;font-size:24px;font-weight:600;">Mensagens por Ler</h2>
<p style="margin:0 0 16px;color:#333;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#333;font-size:16px;line-height:1.6;">
	Tens <strong>%d mensagens não lidas</strong> na tua caixa de entrada. Não deixes passar oportunidades importantes de colaboração.
</p>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:#68007a;color:#ffffff;padding:16px 40px;border-radius:6px;text-decoration:none;font-weight:600;font-size:16px;text-align:center;">
				Ver Mensagens
			</a>
		</td>
	</tr>
</table>
<p style="margin:32px 0 0;color:#666;font-size:14px;line-height:1.6;">
	Acede às tuas <a href="%s" style="color:#68007a;text-decoration:none;">mensagens</a> no SkillBridge.
</p>`

const followNotificationTemplate = `<h2 style="margin:0 0 20px;color:#1a1a1a;font-size:24px;font-weight:600;">Novo Seguidor</h2>
<p style="margin:0 0 16px;color:#333;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#333;font-size:16px;line-height:1.6;">
	<strong>%s</strong> começou a seguir-te no SkillBridge e está interessado no teu trabalho.
</p>
<p style="margin:0 0 32px;color:#666;font-size:15px;line-height:1.6;">
	Visita o perfil para conheceres melhor os projetos e interesses desta pessoa.
</p>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:#68007a;color:#ffffff;padding:16px 40px;border-radius:6px;text-decoration:none;font-weight:600;font-size:16px;text-align:center;">
				Ver Perfil
			</a>
		</td>
	</tr>
</table>
<p style="margin:32px 0 0;color:#666;font-size:14px;line-height:1.6;">
	Continua a explorar a comunidade em <a href="%s" style="color:#68007a;text-decoration:none;">skillbridge.pt</a>.
</p>`

const reviewApprovedTemplate = `<h2 style="margin:0 0 20px;color:#1a1a1a;font-size:24px;font-weight:600;">Avaliação Publicada</h2>
<p style="margin:0 0 16px;color:#333;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#333;font-size:16px;line-height:1.6;">
	A tua avaliação de <strong>%d estrela(s)</strong> para <strong>%s</strong> foi aprovada e está agora visível publicamente.
</p>
<p style="margin:0 0 32px;color:#666;font-size:15px;line-height:1.6;">
	Avaliações autênticas ajudam a comunidade a tomar decisões informadas sobre colaborações.
</p>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:#68007a;color:#ffffff;padding:16px 40px;border-radius:6px;text-decoration:none;font-weight:600;font-size:16px;text-align:center;">
				Ver Perfil Avaliado
			</a>
		</td>
	</tr>
</table>
<p style="margin:32px 0 0;color:#666;font-size:14px;line-height:1.6;">
	Continua em <a href="%s" style="color:#68007a;text-decoration:none;">skillbridge.pt</a>.
</p>`

const projectDecisionApprovedTemplate = `<h2 style="margin:0 0 20px;color:#1a1a1a;font-size:24px;font-weight:600;">Candidatura aprovada ✅</h2>
<p style="margin:0 0 16px;color:#333;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#333;font-size:16px;line-height:1.6;">
	A tua candidatura ao projeto <strong>%s</strong> foi <strong>aprovada</strong>. O <strong>%s</strong> vai agora avançar convosco.
</p>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:#68007a;color:#ffffff;padding:16px 40px;border-radius:6px;text-decoration:none;font-weight:600;font-size:16px;text-align:center;">
				Ver Mensagens
			</a>
		</td>
	</tr>
</table>
<p style="margin:32px 0 0;color:#666;font-size:14px;line-height:1.6;">
	Continua em <a href="%s" style="color:#68007a;text-decoration:none;">skillbridge.pt</a>.
</p>`

const projectDecisionRejectedTemplate = `<h2 style="margin:0 0 20px;color:#1a1a1a;font-size:24px;font-weight:600;">Candidatura rejeitada ❌</h2>
<p style="margin:0 0 16px;color:#333;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#333;font-size:16px;line-height:1.6;">
	Lamentamos informar-te que a tua candidatura ao projeto <strong>%s</strong> foi <strong>rejeitada</strong> pelo <strong>%s</strong>.
</p>
<p style="margin:0 0 24px;color:#666;font-size:14px;line-height:1.6;">
	Podes tentar novamente noutros projetos e continua a explorar oportunidades na plataforma.
</p>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:#68007a;color:#ffffff;padding:16px 40px;border-radius:6px;text-decoration:none;font-weight:600;font-size:16px;text-align:center;">
				Explorar Projetos
			</a>
		</td>
	</tr>
</table>
<p style="margin:32px 0 0;color:#666;font-size:14px;line-height:1.6;">
	Continua em <a href="%s" style="color:#68007a;text-decoration:none;">skillbridge.pt</a>.
</p>`

const projectMatchesTemplate = `<h2 style="margin:0 0 20px;color:#1a1a1a;font-size:24px;font-weight:600;">Projetos Compatíveis</h2>
<p style="margin:0 0 16px;color:#333;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#333;font-size:16px;line-height:1.6;">
	Encontrámos <strong>%d projeto(s)</strong> que correspondem às tuas competências e podem ser do teu interesse.
</p>
<table width="100%%" cellpadding="0" cellspacing="0" style="margin:0 0 32px;width:100%%;">
	%s
</table>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:#68007a;color:#ffffff;padding:16px 40px;border-radius:6px;text-decoration:none;font-weight:600;font-size:16px;text-align:center;">
				Ver Projetos Disponíveis
			</a>
		</td>
	</tr>
</table>
<p style="margin:32px 0 0;color:#666;font-size:14px;line-height:1.6;">
	Explora mais oportunidades em <a href="%s" style="color:#68007a;text-decoration:none;">skillbridge.pt</a>.
</p>`

const perfectProjectTemplate = `<h2 style="margin:0 0 20px;color:#1a1a1a;font-size:24px;font-weight:600;">O Projeto Perfeito para Ti</h2>
<p style="margin:0 0 16px;color:#333;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#333;font-size:16px;line-height:1.6;">
	Acabou de ser criado um novo projeto que corresponde perfeitamente às tuas competências!
</p>
<table width="100%%" cellpadding="0" cellspacing="0" style="margin:0 0 32px;width:100%%;">
	<tr>
		<td style="padding:20px;background:#f8f9fa;border-radius:8px;border:1px solid #e9ecef;">
			<h4 style="margin:0 0 8px;color:#1a1a1a;font-size:16px;font-weight:600;">%s</h4>
			<p style="margin:0 0 12px;color:#4a4a4a;font-size:14px;line-height:1.5;">%s</p>
			<p style="margin:0;color:#6c757d;font-size:13px;">
				<strong>Skills procuradas:</strong> %s
			</p>
		</td>
	</tr>
</table>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:#68007a;color:#ffffff;padding:16px 40px;border-radius:6px;text-decoration:none;font-weight:600;font-size:16px;text-align:center;">
				Ver Detalhes do Projeto
			</a>
		</td>
	</tr>
</table>
<p style="margin:32px 0 0;color:#666;font-size:14px;line-height:1.6;">
	Não percas esta oportunidade. Candidata-te já em <a href="%s" style="color:#68007a;text-decoration:none;">skillbridge.pt</a>.
</p>`

// ─────────────────────────────────────────────────────────────
// RECRUITER EMAIL FUNCTIONS & TEMPLATES
// ─────────────────────────────────────────────────────────────

// SendRecruiterApproved envia email de aprovação com link de acesso (Firebase sign-in link)
func SendRecruiterApproved(name, toEmail, signInLink string) error {
	subject := "A sua conta SkillBridge foi aprovada ✓"
	html := fmt.Sprintf(recruiterApprovedTemplate, name, signInLink, signInLink, signInLink)
	return send(getEmailFrom(), toEmail, subject, html)
}

// SendRecruiterRejected envia email de rejeição ao recrutador
func SendRecruiterRejected(name, toEmail string) error {
	subject := "Atualização sobre o seu pedido — SkillBridge"
	html := fmt.Sprintf(recruiterRejectedTemplate, name)
	return send(getEmailFrom(), toEmail, subject, html)
}

// SendVacancyExpired envia email quando uma vaga expira, com link de renovação
func SendVacancyExpired(name, toEmail, vacancyTitle, renewLink string) error {
	subject := fmt.Sprintf("A sua vaga \"%s\" expirou — SkillBridge", vacancyTitle)
	html := fmt.Sprintf(vacancyExpiredTemplate, name, vacancyTitle, renewLink, renewLink)
	return send(getEmailFrom(), toEmail, subject, html)
}

// SendRecruiterReceived envia confirmação de receção do pedido
func SendRecruiterReceived(name, toEmail string) error {
	subject := "Pedido recebido — SkillBridge"
	html := fmt.Sprintf(recruiterReceivedTemplate, name)
	return send(getEmailFrom(), toEmail, subject, html)
}

const recruiterApprovedTemplate = `<h2 style="margin:0 0 20px;color:#1a1a1a;font-size:24px;font-weight:600;">Conta Aprovada ✓</h2>
<p style="margin:0 0 16px;color:#333;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#333;font-size:16px;line-height:1.6;">
	O seu pedido de acesso ao SkillBridge como recrutador foi <strong>aprovado</strong>. Já pode aceder à plataforma e publicar vagas para estudantes e recém-licenciados.
</p>
<p style="margin:0 0 32px;color:#666;font-size:15px;line-height:1.6;">
	Clique no botão abaixo para aceder à sua conta. Este link é válido por <strong>72 horas</strong> e pode ser utilizado várias vezes durante esse período.
</p>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:#68007a;color:#ffffff;padding:16px 40px;border-radius:6px;text-decoration:none;font-weight:600;font-size:16px;text-align:center;">
				Aceder à Minha Conta
			</a>
		</td>
	</tr>
</table>
<p style="margin:32px 0 0;color:#666;font-size:14px;line-height:1.6;">
	Se o botão não funcionar, copie e cole este link no seu navegador:<br>
	<a href="%s" style="color:#68007a;text-decoration:none;word-break:break-all;">%s</a>
</p>`

const recruiterRejectedTemplate = `<h2 style="margin:0 0 20px;color:#1a1a1a;font-size:24px;font-weight:600;">Atualização do Pedido</h2>
<p style="margin:0 0 16px;color:#333;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#333;font-size:16px;line-height:1.6;">
	Após análise, não nos foi possível aprovar o seu pedido de acesso como recrutador no SkillBridge neste momento.
</p>
<p style="margin:0 0 24px;color:#666;font-size:15px;line-height:1.6;">
	Se acredita que houve um engano ou se tiver informações adicionais, não hesite em contactar-nos respondendo a este email.
</p>
<p style="margin:32px 0 0;color:#666;font-size:14px;line-height:1.6;">
	Obrigado pelo interesse no SkillBridge.
</p>`

const vacancyExpiredTemplate = `<h2 style="margin:0 0 20px;color:#1a1a1a;font-size:24px;font-weight:600;">Vaga Expirada</h2>
<p style="margin:0 0 16px;color:#333;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#333;font-size:16px;line-height:1.6;">
	A sua vaga <strong>"%s"</strong> expirou após 30 dias no SkillBridge. Se pretender manter a vaga ativa, pode renová-la clicando no botão abaixo.
</p>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:#68007a;color:#ffffff;padding:16px 40px;border-radius:6px;text-decoration:none;font-weight:600;font-size:16px;text-align:center;">
				Renovar Vaga
			</a>
		</td>
	</tr>
</table>
<p style="margin:32px 0 0;color:#666;font-size:14px;line-height:1.6;">
	Gira as suas vagas em <a href="%s" style="color:#68007a;text-decoration:none;">skillbridge.pt</a>.
</p>`

const recruiterReceivedTemplate = `<h2 style="margin:0 0 20px;color:#1a1a1a;font-size:24px;font-weight:600;">Pedido Recebido</h2>
<p style="margin:0 0 16px;color:#333;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#333;font-size:16px;line-height:1.6;">
	O seu pedido de acesso como recrutador ao SkillBridge foi recebido com sucesso. A nossa equipa irá analisá-lo e enviar-lhe um email com o resultado em breve.
</p>
<p style="margin:32px 0 0;color:#666;font-size:14px;line-height:1.6;">
	Obrigado pelo interesse no SkillBridge.
</p>`

// SendRecruiterReturning envia email com novo link de acesso para recrutadores que já têm conta aprovada
func SendRecruiterReturning(name, toEmail, accessLink string) error {
	subject := "Novo link de acesso — SkillBridge"
	html := fmt.Sprintf(recruiterReturningTemplate, name, accessLink, accessLink, accessLink)
	return send(getEmailFrom(), toEmail, subject, html)
}

const recruiterReturningTemplate = `<h2 style="margin:0 0 20px;color:#1a1a1a;font-size:24px;font-weight:600;">Novo Link de Acesso</h2>
<p style="margin:0 0 16px;color:#333;font-size:16px;line-height:1.6;">
	Olá <strong>%s</strong>,
</p>
<p style="margin:0 0 24px;color:#333;font-size:16px;line-height:1.6;">
	Recebemos o seu pedido de acesso à conta de recrutador no SkillBridge. Clique no botão abaixo para aceder diretamente à sua conta.
</p>
<p style="margin:0 0 32px;color:#666;font-size:15px;line-height:1.6;">
	Este link é válido por <strong>72 horas</strong> e pode ser utilizado várias vezes durante esse período.
</p>
<table width="100%%" cellpadding="0" cellspacing="0">
	<tr>
		<td align="center">
			<a href="%s" style="display:inline-block;background:#68007a;color:#ffffff;padding:16px 40px;border-radius:6px;text-decoration:none;font-weight:600;font-size:16px;text-align:center;">
				Aceder à Minha Conta
			</a>
		</td>
	</tr>
</table>
<p style="margin:32px 0 0;color:#666;font-size:14px;line-height:1.6;">
	Se o botão não funcionar, copie e cole este link no seu navegador:<br>
	<a href="%s" style="color:#68007a;text-decoration:none;word-break:break-all;">%s</a>
</p>`

