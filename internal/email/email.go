// Package email - DEPRECATED
//
// Este pacote usava Mailgun para envio de emails, mas foi substituído pelo Firebase Auth.
// O Firebase envia emails automaticamente (password reset, email verification, etc.)
// usando templates configuráveis no Firebase Console.
//
// Para emails personalizados no futuro, considere usar:
// - Firebase Extensions (Trigger Email, SendGrid, etc.)
// - Cloud Functions com SendGrid/Mailgun
// - Outro serviço de email transacional
//
// Este ficheiro é mantido apenas como referência histórica.
package email

/*
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
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="margin:0;padding:0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Helvetica Neue',Arial,sans-serif;background:#f3f4f6;">
  <table width="100%%" cellpadding="0" cellspacing="0" style="background:#f3f4f6;padding:40px 20px;">
    <tr>
      <td align="center">
        <table width="600" cellpadding="0" cellspacing="0" style="background:white;border-radius:12px;box-shadow:0 4px 6px rgba(0,0,0,0.1);">
          <tr>
            <td style="padding:40px 40px 24px;text-align:center;border-bottom:1px solid #e5e7eb;">
              <h1 style="margin:0;color:#68007A;font-size:28px;font-weight:700;">SkillBridge</h1>
            </td>
          </tr>
          <tr>
            <td style="padding:40px;">
              <h2 style="margin:0 0 16px;color:#111827;font-size:24px;font-weight:600;">🔐 Redefinir palavra-passe</h2>
              <p style="margin:0 0 16px;color:#374151;font-size:16px;line-height:1.6;">
                Olá, <strong>%s</strong>!
              </p>
              <p style="margin:0 0 24px;color:#374151;font-size:16px;line-height:1.6;">
                Recebemos um pedido para redefinir a palavra-passe da tua conta SkillBridge.
              </p>
              <p style="margin:0 0 32px;color:#6b7280;font-size:14px;line-height:1.6;">
                Clica no botão abaixo para criar uma nova palavra-passe:
              </p>
              <table width="100%%" cellpadding="0" cellspacing="0">
                <tr>
                  <td align="center">
                    <a href="%s"
                       style="display:inline-block;background:linear-gradient(135deg, #68007A 0%%, #9333ea 100%%);
                              color:white;padding:16px 40px;border-radius:8px;text-decoration:none;
                              font-weight:600;font-size:16px;box-shadow:0 4px 6px rgba(104,0,122,0.25);">
                      Redefinir palavra-passe
                    </a>
                  </td>
                </tr>
              </table>
              <div style="margin-top:32px;padding:16px;background:#fef3c7;border-left:4px solid #f59e0b;border-radius:4px;">
                <p style="margin:0;color:#92400e;font-size:14px;line-height:1.6;">
                  ⚠️ <strong>Importante:</strong> Se não fizeste este pedido, ignora este email. O link expira em <strong>1 hora</strong>.
                </p>
              </div>
            </td>
          </tr>
          <tr>
            <td style="padding:24px 40px;background:#f9fafb;border-top:1px solid #e5e7eb;border-radius:0 0 12px 12px;">
              <p style="margin:0;color:#6b7280;font-size:13px;text-align:center;">
                Equipa SkillBridge<br>
                <span style="font-size:12px;color:#9ca3af;">Este é um email automático, por favor não respondas.</span>
              </p>
            </td>
          </tr>
        </table>
      </td>
    </tr>
  </table>
</body>
</html>`, name, resetLink)
	return send(to, subject, html)
}
*/

