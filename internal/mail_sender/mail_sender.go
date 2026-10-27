package mail_sender

import (
	"fmt"
	"mime"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
)

type MailKind string

type MailSenderConfig struct {
	Host     string
	Email    string
	Password string
	MailsDir string
}

type MailSender struct {
	From              string
	Host              string
	Auth              smtp.Auth
	ResetPasswordMail MailKind
	AnnouncementMail  MailKind
}

func New(cfg *MailSenderConfig) *MailSender {
	auth := smtp.PlainAuth("", cfg.Email, cfg.Password, cfg.Host)

	return &MailSender{
		Host:              cfg.Host,
		Auth:              auth,
		From:              cfg.Email,
		ResetPasswordMail: MailKind(filepath.Join(cfg.MailsDir, "reset-password.html")),
		AnnouncementMail:  MailKind(filepath.Join(cfg.MailsDir, "announcement.html")),
	}
}

// Render fills a template's {{KEY}} placeholders with vars. Values are
// inserted as-is: callers escape anything that isn't trusted HTML.
func (m *MailSender) Render(kind MailKind, vars map[string]string) (string, error) {
	bytesContent, err := os.ReadFile(string(kind))
	if err != nil {
		return "", err
	}
	return fillTemplate(string(bytesContent), vars), nil
}

// fillTemplate replaces every placeholder in one pass, so a value containing
// "{{KEY}}" is never expanded a second time.
func fillTemplate(template string, vars map[string]string) string {
	pairs := make([]string, 0, len(vars)*2)
	for k, v := range vars {
		pairs = append(pairs, fmt.Sprintf("{{%s}}", k), v)
	}
	return strings.NewReplacer(pairs...).Replace(template)
}

func (m *MailSender) SendMail(subject string, to []string, kind MailKind, vars map[string]string) error {
	body, err := m.Render(kind, vars)
	if err != nil {
		return err
	}
	content := buildMessage(m.From, to, subject, body)
	return smtp.SendMail(m.Host+":587", m.Auth, m.From, to, []byte(content))
}

func buildMessage(from string, to []string, subject, body string) string {
	return fmt.Sprintf("From: %s <%s>\r\n", mime.QEncoding.Encode("utf-8", "Sauté"), from) +
		fmt.Sprintf("To: %s\r\n", stripNewlines(strings.Join(to, ", "))) +
		fmt.Sprintf("Subject: %s\r\n", mime.QEncoding.Encode("utf-8", stripNewlines(subject))) +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" +
		body
}

// stripNewlines keeps a header value from injecting extra headers.
func stripNewlines(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}
