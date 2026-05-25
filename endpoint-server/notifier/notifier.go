package notifier

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/smtp"
	"strings"

	"agent_patches/endpoint-server/config"
)

// Sink sends a notification to an external service.
type Sink interface {
	Send(ctx context.Context, subject, body string) error
}

// Notifier fans a notification out to all configured sinks.
// A nil Notifier is valid and silently discards all notifications.
type Notifier struct {
	sinks []Sink
}

// New constructs a Notifier from settings, enabling each sink that is configured.
func New(cfg *config.NotifierSettings) *Notifier {
	n := &Notifier{}
	if cfg.Email.Enabled {
		n.sinks = append(n.sinks, newEmailSink(&cfg.Email))
		slog.Info("notifier: email sink enabled", "host", cfg.Email.Host, "to", cfg.Email.To)
	}
	return n
}

// Notify sends subject and body to every configured sink. Errors are logged
// but do not block delivery to remaining sinks.
func (n *Notifier) Notify(ctx context.Context, subject, body string) {
	if n == nil {
		return
	}
	if len(n.sinks) == 0 {
		slog.Warn("notifier: notification dropped, no sinks configured", "subject", subject)
		return
	}
	for _, s := range n.sinks {
		if err := s.Send(ctx, subject, body); err != nil {
			slog.Warn("notifier: sink delivery failed", "error", err)
		}
	}
}

// emailSink delivers notifications via SMTP.
// TLSMode controls the transport:
//   - "" or "starttls" — use smtp.SendMail which negotiates STARTTLS (port 587 typical)
//   - "tls"            — open a TLS connection directly before the SMTP handshake (port 465 typical)
//   - "none"           — plaintext, no encryption (only for local/testing servers)
type emailSink struct {
	cfg *config.EmailNotifierSettings
}

func newEmailSink(cfg *config.EmailNotifierSettings) *emailSink {
	return &emailSink{cfg: cfg}
}

func (e *emailSink) Send(_ context.Context, subject, body string) error {
	slog.Debug("notifier: Notify called",
		"to", strings.Join(e.cfg.To, ", "),
		"from", e.cfg.From,
		"subject", subject,
		"body", body,
	)
	msg := buildMessage(e.cfg.From, e.cfg.To, subject, body)
	addr := fmt.Sprintf("%s:%d", e.cfg.Host, e.cfg.Port)

	switch e.cfg.TLSMode {
	case "tls":
		return e.sendImplicitTLS(addr, msg)
	case "none":
		return e.sendPlain(addr, msg)
	default: // "starttls" or unset
		auth := smtp.PlainAuth("", e.cfg.Username, e.cfg.Password, e.cfg.Host)
		if err := smtp.SendMail(addr, auth, e.cfg.From, e.cfg.To, msg); err != nil {
			return fmt.Errorf("email: send: %w", err)
		}
		return nil
	}
}

// sendImplicitTLS wraps the TCP connection in TLS before the SMTP handshake
// (implicit TLS / SMTPS, typically port 465).
func (e *emailSink) sendImplicitTLS(addr string, msg []byte) error {
	host, _, _ := net.SplitHostPort(addr)
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return fmt.Errorf("email: tls dial: %w", err)
	}

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("email: smtp client: %w", err)
	}
	defer c.Close()

	if e.cfg.Username != "" {
		auth := smtp.PlainAuth("", e.cfg.Username, e.cfg.Password, host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("email: auth: %w", err)
		}
	}
	return sendViaClient(c, e.cfg.From, e.cfg.To, msg)
}

// sendPlain sends over a plaintext connection (no TLS). Only suitable for
// localhost or internal relay servers that do not require authentication.
func (e *emailSink) sendPlain(addr string, msg []byte) error {
	host, _, _ := net.SplitHostPort(addr)
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("email: dial: %w", err)
	}
	defer c.Close()

	if err := c.Hello(host); err != nil {
		return fmt.Errorf("email: EHLO: %w", err)
	}
	return sendViaClient(c, e.cfg.From, e.cfg.To, msg)
}

func sendViaClient(c *smtp.Client, from string, to []string, msg []byte) error {
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("email: MAIL FROM: %w", err)
	}
	for _, addr := range to {
		if err := c.Rcpt(addr); err != nil {
			return fmt.Errorf("email: RCPT TO %s: %w", addr, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("email: DATA: %w", err)
	}
	defer w.Close()
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("email: write body: %w", err)
	}
	return nil
}

func buildMessage(from string, to []string, subject, body string) []byte {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return []byte(sb.String())
}
