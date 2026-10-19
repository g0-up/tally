package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// SMTPConfig configures the SMTP adapter.
type SMTPConfig struct {
	Host     string
	Port     int
	User     string // empty: no authentication (Mailpit in dev)
	Password string
	From     *mail.Address
	// Timeout bounds dialing plus the whole SMTP conversation (default 30 s).
	Timeout time.Duration
}

// SMTP sends mail through an SMTP relay. STARTTLS is used whenever the server offers it and is
// mandatory when credentials are configured, so a password never crosses the network in clear.
type SMTP struct {
	cfg SMTPConfig
	now func() time.Time
}

var _ Mailer = (*SMTP)(nil)

// NewSMTP validates cfg and returns the adapter.
func NewSMTP(cfg SMTPConfig) (*SMTP, error) {
	if cfg.Host == "" || cfg.Port <= 0 {
		return nil, errors.New("smtp: host and port are required")
	}
	if cfg.From == nil || cfg.From.Address == "" {
		return nil, errors.New("smtp: from address is required")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &SMTP{cfg: cfg, now: time.Now}, nil
}

// Send implements Mailer.
func (s *SMTP) Send(ctx context.Context, msg Message) error {
	if err := msg.Validate(); err != nil {
		return err
	}
	body, err := s.build(msg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp: dial %s: %w", addr, err)
	}
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp: %w", err)
	}
	c, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp: greeting: %w", err)
	}
	defer func() { _ = c.Close() }()

	if err := s.converse(c, msg, body); err != nil {
		return err
	}
	return nil
}

func (s *SMTP) converse(c *smtp.Client, msg Message, body []byte) error {
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp: starttls: %w", err)
		}
	} else if s.cfg.User != "" {
		return errors.New("smtp: server does not offer STARTTLS; refusing to send credentials")
	}
	if s.cfg.User != "" {
		if err := c.Auth(smtp.PlainAuth("", s.cfg.User, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("smtp: auth: %w", err)
		}
	}
	if err := c.Mail(s.cfg.From.Address); err != nil {
		return fmt.Errorf("smtp: MAIL FROM: %w", err)
	}
	for _, to := range msg.To {
		if err := c.Rcpt(to.Address); err != nil {
			return fmt.Errorf("smtp: RCPT TO: %w", err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp: write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: end of data: %w", err)
	}
	if err := c.Quit(); err != nil {
		return fmt.Errorf("smtp: quit: %w", err)
	}
	return nil
}

// build renders the RFC 5322 message: UTF-8 headers encoded per RFC 2047, quoted-printable
// bodies, multipart/alternative when an HTML part exists.
func (s *SMTP) build(msg Message) ([]byte, error) {
	var buf bytes.Buffer
	to := make([]string, len(msg.To))
	for i, a := range msg.To {
		to[i] = a.String()
	}
	msgID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	domain := s.cfg.From.Address[strings.LastIndexByte(s.cfg.From.Address, '@')+1:]
	header := func(k, v string) { buf.WriteString(k + ": " + v + "\r\n") }
	header("From", s.cfg.From.String())
	header("To", strings.Join(to, ", "))
	header("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	header("Date", s.now().Format(time.RFC1123Z))
	header("Message-ID", "<"+msgID+"@"+domain+">")
	header("MIME-Version", "1.0")

	if msg.HTML == "" {
		header("Content-Type", `text/plain; charset="utf-8"`)
		header("Content-Transfer-Encoding", "quoted-printable")
		buf.WriteString("\r\n")
		if err := writeQP(&buf, msg.Text); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	boundary, err := randomHex(12)
	if err != nil {
		return nil, err
	}
	header("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	buf.WriteString("\r\n")
	for _, part := range []struct{ ctype, body string }{{"text/plain", msg.Text}, {"text/html", msg.HTML}} {
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString(`Content-Type: ` + part.ctype + `; charset="utf-8"` + "\r\n")
		buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		if err := writeQP(&buf, part.body); err != nil {
			return nil, err
		}
		buf.WriteString("\r\n")
	}
	buf.WriteString("--" + boundary + "--\r\n")
	return buf.Bytes(), nil
}

func writeQP(buf *bytes.Buffer, s string) error {
	w := quotedprintable.NewWriter(buf)
	if _, err := w.Write([]byte(strings.ReplaceAll(s, "\r\n", "\n"))); err != nil {
		return fmt.Errorf("smtp: encode body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: encode body: %w", err)
	}
	return nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("smtp: random: %w", err)
	}
	return hex.EncodeToString(b), nil
}
