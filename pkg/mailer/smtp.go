package mailer

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net/smtp"
	"strings"
	"time"
)

// SMTPConfig configures an SMTP transport.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     Address
	UseTLS   bool
	Timeout  time.Duration
}

// SMTP is the bare-metal transport. It builds a MIME multipart/alternative
// body when both Text and HTML are set, otherwise sends a single part.
type SMTP struct{ cfg SMTPConfig }

// NewSMTP returns an SMTP mailer.
func NewSMTP(cfg SMTPConfig) *SMTP { return &SMTP{cfg: cfg} }

// Send implements Mailer.
func (s *SMTP) Send(ctx context.Context, m Message) error {
	if len(m.To)+len(m.CC)+len(m.BCC) == 0 {
		return ErrNoRecipients
	}
	if m.From.Email == "" {
		m.From = s.cfg.From
	}
	if m.From.Email == "" {
		return errors.New("mailer: From is empty")
	}

	body, contentType, err := encode(m)
	if err != nil {
		return err
	}
	headers := make([]string, 0, 8+len(m.Headers))
	headers = append(headers,
		"From: "+formatAddr(m.From),
		"To: "+joinAddrs(m.To),
		"Subject: "+encodeSubject(m.Subject),
		"MIME-Version: 1.0",
		"Content-Type: "+contentType,
		"Date: "+time.Now().UTC().Format(time.RFC1123Z),
	)
	if len(m.CC) > 0 {
		headers = append(headers, "Cc: "+joinAddrs(m.CC))
	}
	if m.ReplyTo != nil {
		headers = append(headers, "Reply-To: "+formatAddr(*m.ReplyTo))
	}
	for k, v := range m.Headers {
		headers = append(headers, k+": "+v)
	}
	full := []byte(strings.Join(headers, "\r\n") + "\r\n\r\n" + body)

	addr := fmt.Sprintf("%s:%d", s.cfg.Host, s.cfg.Port)
	auth := smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)
	rcpts := make([]string, 0, len(m.To)+len(m.CC)+len(m.BCC))
	for _, a := range append(append(m.To, m.CC...), m.BCC...) {
		rcpts = append(rcpts, a.Email)
	}

	// SMTP packages do not support context cancellation directly.
	// We honour the deadline by enforcing a short connect timeout
	// and an overall timeout via dialer; callers with strict SLA
	// should use a job queue.
	if s.cfg.UseTLS {
		return sendTLS(ctx, addr, s.cfg.Host, auth, m.From.Email, rcpts, full)
	}
	if err := smtp.SendMail(addr, auth, m.From.Email, rcpts, full); err != nil {
		return fmt.Errorf("smtp: %w", err)
	}
	return nil
}

func sendTLS(ctx context.Context, addr, host string, auth smtp.Auth, from string, to []string, body []byte) error {
	d := &tls.Dialer{NetDialer: dialerWithCtx(ctx)}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if err := c.Auth(auth); err != nil {
		return err
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, r := range to {
		if err := c.Rcpt(r); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func encode(m Message) (string, string, error) {
	hasText := m.Text != ""
	hasHTML := m.HTML != ""
	hasAttach := len(m.Attachments) > 0

	if !hasText && !hasHTML {
		return "", "", errors.New("mailer: empty body")
	}

	if !hasAttach && hasText && !hasHTML {
		return qpEncode(m.Text), `text/plain; charset="utf-8"; format="flowed"` + "\r\nContent-Transfer-Encoding: quoted-printable", nil
	}
	if !hasAttach && hasHTML && !hasText {
		return qpEncode(m.HTML), `text/html; charset="utf-8"` + "\r\nContent-Transfer-Encoding: quoted-printable", nil
	}

	boundary := "goforge-" + randHex(12)
	var buf bytes.Buffer
	writePart := func(ctype, body string) {
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString("Content-Type: " + ctype + "\r\n")
		buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n\r\n")
		buf.WriteString(qpEncode(body))
		buf.WriteString("\r\n")
	}
	if hasText {
		writePart(`text/plain; charset="utf-8"`, m.Text)
	}
	if hasHTML {
		writePart(`text/html; charset="utf-8"`, m.HTML)
	}
	for _, a := range m.Attachments {
		body, err := io.ReadAll(a.Body)
		if err != nil {
			return "", "", err
		}
		buf.WriteString("--" + boundary + "\r\n")
		buf.WriteString("Content-Type: " + a.ContentType + "; name=\"" + a.Filename + "\"\r\n")
		buf.WriteString("Content-Transfer-Encoding: base64\r\n")
		buf.WriteString("Content-Disposition: attachment; filename=\"" + a.Filename + "\"\r\n\r\n")
		buf.WriteString(base64.StdEncoding.EncodeToString(body))
		buf.WriteString("\r\n")
	}
	buf.WriteString("--" + boundary + "--\r\n")

	subType := "alternative"
	if hasAttach {
		subType = "mixed"
	}
	return buf.String(), `multipart/` + subType + `; boundary="` + boundary + `"`, nil
}

func qpEncode(s string) string {
	var b bytes.Buffer
	w := quotedprintable.NewWriter(&b)
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	return b.String()
}

func formatAddr(a Address) string {
	if a.Name == "" {
		return a.Email
	}
	return fmt.Sprintf(`%q <%s>`, a.Name, a.Email)
}

func joinAddrs(as []Address) string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = formatAddr(a)
	}
	return strings.Join(out, ", ")
}

func encodeSubject(s string) string {
	// RFC 2047 base64-encoded UTF-8 only when non-ASCII is
	// present. Plain ASCII goes through verbatim.
	for _, r := range s {
		if r > 127 {
			return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
		}
	}
	return s
}
