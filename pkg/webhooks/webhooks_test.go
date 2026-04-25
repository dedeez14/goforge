package webhooks

import (
	"strings"
	"testing"
	"time"
)

func TestSign_RoundTripVerifies(t *testing.T) {
	t.Parallel()
	body := []byte(`{"hello":"world"}`)
	sig := Sign("topsecret", "evt_1", body, time.Now())
	if err := VerifySignature("topsecret", "evt_1", body, sig); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerify_BadSignatureRejected(t *testing.T) {
	t.Parallel()
	body := []byte(`x`)
	sig := Sign("a", "evt", body, time.Now())
	if err := VerifySignature("b", "evt", body, sig); err == nil {
		t.Fatal("verify with wrong secret should fail")
	}
}

func TestVerify_TamperedBodyRejected(t *testing.T) {
	t.Parallel()
	body := []byte(`good`)
	sig := Sign("s", "evt", body, time.Now())
	if err := VerifySignature("s", "evt", []byte(`evil`), sig); err == nil {
		t.Fatal("tampered body must fail verify")
	}
}

func TestVerify_OldTimestampRejected(t *testing.T) {
	t.Parallel()
	body := []byte(`x`)
	sig := Sign("s", "e", body, time.Now().Add(-2*time.Hour))
	if err := VerifySignature("s", "e", body, sig); err == nil {
		t.Fatal("stale timestamp must fail verify")
	}
}

func TestVerify_AcceptsMultipleV1Candidates(t *testing.T) {
	t.Parallel()
	body := []byte(`payload`)
	sig := Sign("real", "e", body, time.Now())
	// craft a header that includes a bogus v1 first
	header := strings.Replace(sig, "v1=", "v1=deadbeef,v1=", 1)
	if err := VerifySignature("real", "e", body, header); err != nil {
		t.Fatalf("verify with bogus+real candidate: %v", err)
	}
}
