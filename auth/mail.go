package auth

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AhmadRaza-Khan/semii/config"
)

// Sender delivers account emails. The full mail package arrives in v2.
type Sender interface {
	SendVerification(ctx context.Context, to, link string) error
	SendPasswordReset(ctx context.Context, to, link string) error
}

// LogSender logs links instead of sending mail. The links carry secret
// tokens, so use it in development and tests only.
type LogSender struct{ Log *slog.Logger }

// SendVerification implements Sender.
func (s LogSender) SendVerification(_ context.Context, to, link string) error {
	s.Log.Info("email: verify address (LogSender; not sent)", "to", to, "link", link)
	return nil
}

// SendPasswordReset implements Sender.
func (s LogSender) SendPasswordReset(_ context.Context, to, link string) error {
	s.Log.Info("email: reset password (LogSender; not sent)", "to", to, "link", link)
	return nil
}

// Mail is one message recorded by MemorySender.
type Mail struct {
	Kind string // "verification" | "reset"
	To   string
	Link string
}

// MemorySender records messages for tests.
type MemorySender struct {
	mu   sync.Mutex
	sent []Mail
}

// SendVerification implements Sender.
func (m *MemorySender) SendVerification(_ context.Context, to, link string) error {
	m.record(Mail{"verification", to, link})
	return nil
}

// SendPasswordReset implements Sender.
func (m *MemorySender) SendPasswordReset(_ context.Context, to, link string) error {
	m.record(Mail{"reset", to, link})
	return nil
}

func (m *MemorySender) record(mail Mail) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, mail)
}

// Sent returns the messages recorded so far.
func (m *MemorySender) Sent() []Mail {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Mail(nil), m.sent...)
}

// Last returns the most recent message of kind to address, if any.
func (m *MemorySender) Last(kind, to string) (Mail, bool) {
	sent := m.Sent()
	for i := len(sent) - 1; i >= 0; i-- {
		if sent[i].Kind == kind && sent[i].To == to {
			return sent[i], true
		}
	}
	return Mail{}, false
}

// SMTPConfig is loaded from SMTP_* variables (envPrefix:"SMTP_"). Sending
// is disabled while Host is empty.
type SMTPConfig struct {
	Host     string        `env:"HOST"`
	Port     int           `env:"PORT" default:"587"`
	Username string        `env:"USERNAME"`
	Password config.Secret `env:"PASSWORD"`
	From     string        `env:"FROM" default:"no-reply@localhost"`
	Timeout  time.Duration `env:"TIMEOUT" default:"10s"`
}

// SMTPSender sends plain-text mail with net/smtp. STARTTLS is required:
// servers that do not offer it are refused, so credentials and links never
// travel in clear text.
type SMTPSender struct {
	Config SMTPConfig
	// TLS overrides the TLS settings (tests); ServerName defaults to Host.
	TLS *tls.Config
}

// SendVerification implements Sender.
func (s SMTPSender) SendVerification(ctx context.Context, to, link string) error {
	return s.send(ctx, to, "Confirm your email address",
		"Confirm your email address by opening this link within 24 hours:\n\n"+link+"\n\nIf you did not sign up, ignore this message.\n")
}

// SendPasswordReset implements Sender.
func (s SMTPSender) SendPasswordReset(ctx context.Context, to, link string) error {
	return s.send(ctx, to, "Reset your password",
		"Reset your password by opening this link within 1 hour:\n\n"+link+"\n\nIf you did not ask for this, ignore this message.\n")
}

func (s SMTPSender) send(ctx context.Context, to, subject, body string) error {
	c := s.Config
	if c.Host == "" {
		return errors.New("auth: SMTP_HOST is not set")
	}
	if strings.ContainsAny(to+c.From+subject, "\r\n") {
		return errors.New("auth: invalid mail header")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("auth: smtp dial: %w", err)
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	client, err := smtp.NewClient(conn, c.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("auth: smtp: %w", err)
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return errors.New("auth: smtp server does not offer STARTTLS; refusing to send")
	}
	tlsCfg := s.TLS
	if tlsCfg == nil {
		tlsCfg = &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	}
	if err := client.StartTLS(tlsCfg); err != nil {
		return fmt.Errorf("auth: smtp starttls: %w", err)
	}
	if c.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", c.Username, c.Password.Reveal(), c.Host)); err != nil {
			return fmt.Errorf("auth: smtp auth: %w", err)
		}
	}
	if err := client.Mail(c.From); err != nil {
		return fmt.Errorf("auth: smtp from: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("auth: smtp rcpt: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("auth: smtp data: %w", err)
	}
	msg := "From: " + c.From + "\r\nTo: " + to + "\r\nSubject: " + subject +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
		strings.ReplaceAll(body, "\n", "\r\n")
	if _, err := w.Write([]byte(msg)); err != nil {
		return fmt.Errorf("auth: smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("auth: smtp send: %w", err)
	}
	return client.Quit()
}
