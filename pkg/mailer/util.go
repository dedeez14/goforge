package mailer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net"
	"time"
)

func dialerWithCtx(ctx context.Context) *net.Dialer {
	d := &net.Dialer{Timeout: 30 * time.Second}
	if dl, ok := ctx.Deadline(); ok {
		d.Deadline = dl
	}
	return d
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
