// Package mailer is goforge's email abstraction.
//
// It treats sending mail as a queueable side-effect: handlers
// describe a Message (recipients, subject, html/text body, optional
// attachments), the Mailer transport delivers it. Different
// transports plug in for different deployment models:
//
//   - SMTP — minimum viable, works with any provider that exposes
//     submission (Gmail, Postmark, Mailgun SMTP, Postfix relay).
//   - LogTransport — prints messages to the logger; used in dev so
//     people don't have to spin up a relay just to test signup
//     flows.
//   - Memory — keeps messages in a slice; used in tests so callers
//     can assert against what was sent.
//
// Templating is deliberately *outside* the transport — call
// templates.RenderHTML(...) yourself, or wire it into a job. The
// Mailer interface is purely "deliver this rendered message".
package mailer

import (
	"context"
	"errors"
	"io"
)

// Address is a single recipient or sender. Name is optional.
type Address struct {
	Name  string
	Email string
}

// Attachment is one file attached to the message.
type Attachment struct {
	Filename    string
	ContentType string
	Body        io.Reader
}

// Message is the payload accepted by every Mailer.
type Message struct {
	From        Address
	To          []Address
	CC          []Address
	BCC         []Address
	ReplyTo     *Address
	Subject     string
	Text        string
	HTML        string
	Attachments []Attachment

	// Headers is appended verbatim. Use it for List-Unsubscribe,
	// X-Tags, etc.
	Headers map[string]string
}

// ErrNoRecipients is returned when Send is given a Message with an
// empty To/CC/BCC.
var ErrNoRecipients = errors.New("mailer: message has no recipients")

// Mailer delivers Messages.
type Mailer interface {
	Send(ctx context.Context, m Message) error
}
