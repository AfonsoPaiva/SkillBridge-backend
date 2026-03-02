package email

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/mailgun/mailgun-go/v4"
	"github.com/paiva/SkillBridge/Backend/config"
)

func send(to, subject, html string) error {
	cfg := config.AppConfig
	if cfg.MailgunAPIKey == "" || cfg.MailgunDomain == "" {
		log.Printf("[email] Mailgun não configurado — email para %s ignorado", to)
		return nil
	}

	mg := mailgun.NewMailgun(cfg.MailgunDomain, cfg.MailgunAPIKey)
	m := mg.NewMessage(cfg.MailgunSender, subject, "", to)
	m.SetHtml(html)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, id, err := mg.Send(ctx, m)
	log.Printf("[email] Mailgun send to=%s id=%q resp=%q err=%v", to, id, resp, err)
	return err
}

// SendWelcome envia um email de boas-vindas após registo.
func SendWelcome(name, to string) error {
	subject := "Bem-vindo ao SkillBridge! 🎉"
	html := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;max-width:600px;margin:auto;padding:24px;">
  <h1 style="color:#4F46E5;">Olá, %s! 👋</h1>
  <p>Obrigado por te registares no <strong>SkillBridge</strong>.</p>
  <p>A nossa plataforma liga estudantes a projetos colaborativos reais.
     Explora projetos disponíveis, candidata-te e começa a construir o teu portfólio.</p>
  <br>
  <p style="color:#6B7280;">Equipa SkillBridge</p>
</body>
</html>`, name)
	return send(to, subject, html)
}

// SendPasswordReset envia o link de redefinição de palavra-passe gerado pelo Firebase.
func SendPasswordReset(name, to, resetLink string) error {
	subject := "Redefinir palavra-passe — SkillBridge"
	html := fmt.Sprintf(`
<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;max-width:600px;margin:auto;padding:24px;">
  <h1 style="color:#4F46E5;">Redefinir palavra-passe</h1>
  <p>Olá, <strong>%s</strong>!</p>
  <p>Recebemos um pedido para redefinir a palavra-passe da tua conta SkillBridge.</p>
  <p style="margin:32px 0;">
    <a href="%s"
       style="background:#4F46E5;color:white;padding:14px 28px;border-radius:8px;
              text-decoration:none;font-weight:bold;">
      Redefinir palavra-passe
    </a>
  </p>
  <p style="color:#6B7280;font-size:13px;">
    Se não fizeste este pedido, podes ignorar este email com segurança.<br>
    O link expira em 1 hora.
  </p>
  <br>
  <p style="color:#6B7280;">Equipa SkillBridge</p>
</body>
</html>`, name, resetLink)
	return send(to, subject, html)
}
