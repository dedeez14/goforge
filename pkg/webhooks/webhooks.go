// Package webhooks provides outgoing-webhook delivery and inbound
// signature verification for goforge.
//
// Outgoing flow:
//
//	storage    → Endpoint(id, url, secret, events[], active)
//	delivery   → Webhook(event_id, endpoint_id) enqueued onto pkg/jobs
//	signing    → HMAC-SHA256(secret, "t.<timestamp>.<event_id>.<body>")
//	header     → "Webhook-Signature: t=<timestamp>,v1=<hex-signature>"
//
// The signature scheme is the same shape Stripe and Slack use (a
// timestamp prefix to prevent replay + a hex MAC). Receivers verify
// by computing the same MAC over the request body using a shared
// secret; goforge ships VerifySignature so receivers using goforge
// don't have to implement the math themselves.
//
// Outgoing deliveries are written through pkg/jobs so retries,
// backoff and DLQ behaviour are uniform with the rest of the
// framework — no second retry strategy to reason about.
package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignatureHeader is the canonical HTTP header carrying the signature.
const SignatureHeader = "Webhook-Signature"

// MaxAge is how far in the past a timestamp may be before we treat
// the request as a replay. Five minutes mirrors the Stripe default
// and is plenty for honest clients with mild clock drift.
const MaxAge = 5 * time.Minute

// Sign computes the canonical "t=<unix>,v1=<hex>" header value for
// a given body. timestamp can be the zero Time to use time.Now.
func Sign(secret string, eventID string, body []byte, timestamp time.Time) string {
	if timestamp.IsZero() {
		timestamp = time.Now().UTC()
	}
	ts := strconv.FormatInt(timestamp.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts))
	mac.Write([]byte{'.'})
	mac.Write([]byte(eventID))
	mac.Write([]byte{'.'})
	mac.Write(body)
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifySignature checks the supplied header against the body using
// secret. It rejects replays older than MaxAge and signatures that
// don't match in constant time. Multiple v1 candidates are accepted
// in one header so secret rotation works without downtime.
func VerifySignature(secret, eventID string, body []byte, header string) error {
	parts := strings.Split(header, ",")
	if len(parts) < 2 {
		return errors.New("webhooks: malformed signature header")
	}
	var (
		ts        int64
		sigs      []string
		found     bool
	)
	for _, p := range parts {
		kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			n, err := strconv.ParseInt(kv[1], 10, 64)
			if err != nil {
				return fmt.Errorf("webhooks: bad timestamp: %w", err)
			}
			ts = n
			found = true
		case "v1":
			sigs = append(sigs, kv[1])
		}
	}
	if !found {
		return errors.New("webhooks: missing timestamp")
	}
	if len(sigs) == 0 {
		return errors.New("webhooks: missing v1 signature")
	}

	if abs := time.Since(time.Unix(ts, 0)); abs < 0 {
		// Future timestamp — likely clock skew, but still
		// suspicious past a small window.
		if -abs > MaxAge {
			return errors.New("webhooks: timestamp too far in the future")
		}
	} else if abs > MaxAge {
		return errors.New("webhooks: timestamp too old")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte{'.'})
	mac.Write([]byte(eventID))
	mac.Write([]byte{'.'})
	mac.Write(body)
	want := mac.Sum(nil)
	for _, s := range sigs {
		got, err := hex.DecodeString(s)
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare(got, want) == 1 {
			return nil
		}
	}
	return errors.New("webhooks: signature mismatch")
}
