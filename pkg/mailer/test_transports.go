package mailer

import (
	"context"
	"sync"

	"github.com/rs/zerolog"
)

// LogTransport prints every message to the logger and returns nil.
// Useful in dev so signup/forgot-password flows can be exercised
// end-to-end without wiring an SMTP relay.
type LogTransport struct{ Logger zerolog.Logger }

// Send implements Mailer.
func (l LogTransport) Send(_ context.Context, m Message) error {
	rcpts := make([]string, 0, len(m.To))
	for _, a := range m.To {
		rcpts = append(rcpts, a.Email)
	}
	l.Logger.Info().
		Strs("to", rcpts).
		Str("from", m.From.Email).
		Str("subject", m.Subject).
		Int("text_len", len(m.Text)).
		Int("html_len", len(m.HTML)).
		Int("attachments", len(m.Attachments)).
		Msg("mail (not sent — log transport)")
	return nil
}

// MemoryTransport keeps every Send call in-memory so tests can
// assert against what would have been sent.
type MemoryTransport struct {
	mu       sync.Mutex
	Messages []Message
}

// Send implements Mailer.
func (m *MemoryTransport) Send(_ context.Context, msg Message) error {
	if len(msg.To)+len(msg.CC)+len(msg.BCC) == 0 {
		return ErrNoRecipients
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Messages = append(m.Messages, msg)
	return nil
}

// Snapshot returns a copy of the recorded messages, safe to inspect
// from another goroutine.
func (m *MemoryTransport) Snapshot() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Message, len(m.Messages))
	copy(out, m.Messages)
	return out
}
