package mailer

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSMTP_EncodeMultipart(t *testing.T) {
	t.Parallel()
	body, ctype, err := encode(Message{
		Text: "plain body",
		HTML: "<b>html body</b>",
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(ctype, "multipart/alternative") {
		t.Fatalf("unexpected content-type %q", ctype)
	}
	if !strings.Contains(body, "text/plain") || !strings.Contains(body, "text/html") {
		t.Fatalf("expected both parts, got: %s", body)
	}
}

func TestSMTP_EmptyBodyRejected(t *testing.T) {
	t.Parallel()
	if _, _, err := encode(Message{}); err == nil {
		t.Fatal("encode must reject empty body")
	}
}

func TestSMTP_NoRecipientsReturnsErr(t *testing.T) {
	t.Parallel()
	s := NewSMTP(SMTPConfig{Host: "x", Port: 25, From: Address{Email: "from@x"}})
	if err := s.Send(context.Background(), Message{Subject: "hi", Text: "x"}); !errors.Is(err, ErrNoRecipients) {
		t.Fatalf("expected ErrNoRecipients, got %v", err)
	}
}

func TestMemoryTransport_RecordsMessages(t *testing.T) {
	t.Parallel()
	m := &MemoryTransport{}
	msg := Message{To: []Address{{Email: "a@b"}}, Subject: "x", Text: "y"}
	if err := m.Send(context.Background(), msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := m.Snapshot(); len(got) != 1 || got[0].Subject != "x" {
		t.Fatalf("unexpected: %+v", got)
	}
}
