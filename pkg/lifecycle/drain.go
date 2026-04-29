// Package lifecycle coordinates graceful shutdown across the HTTP
// server, background workers, and readiness probes.
//
// The key problem it solves: when a Kubernetes pod is terminated,
// there is a window between SIGTERM and the pod's endpoint being
// removed from the Service (kube-proxy takes a few hundred ms to
// propagate). If the HTTP server starts refusing connections
// immediately, any request that lands during that window produces a
// 5xx. The fix is a three-phase shutdown:
//
//  1. StartDraining()  → /readyz starts returning 503. Kubernetes
//     observes this and removes the pod from the
//     Service endpoints within one probe interval.
//     The HTTP server keeps accepting new requests during this grace
//     window so in-flight traffic continues to land on 2xx.
//  2. GracePeriod      → Sleep for cfg.HTTP.DrainGracePeriod to let
//     kube-proxy catch up. 5s is the default.
//  3. app.Shutdown()   → Fiber refuses new connections and waits for
//     in-flight handlers to return. Workers get
//     their own context cancelled.
//
// Drainer is safe for concurrent use; multiple goroutines can call
// IsDraining() without a lock.
package lifecycle

import "sync/atomic"

// Drainer is a tiny boolean flag indicating whether the process has
// entered its pre-shutdown drain phase. The health handler consults
// IsDraining() to decide whether /readyz should report 200 or 503.
//
// A value of zero means "serving"; any non-zero value means
// "draining". The actual integer is unimportant - we use atomic.Int32
// rather than a sync/atomic bool to stay compatible with Go versions
// that do not have atomic.Bool.
type Drainer struct {
	draining atomic.Int32
}

// NewDrainer returns a Drainer in the serving (non-draining) state.
func NewDrainer() *Drainer { return &Drainer{} }

// StartDraining flips the flag. Subsequent IsDraining calls return
// true. Calling StartDraining repeatedly is idempotent.
func (d *Drainer) StartDraining() {
	d.draining.Store(1)
}

// IsDraining reports whether the drain phase has begun. Hot-path
// callers (/readyz) hit this on every probe; it must be cheap.
func (d *Drainer) IsDraining() bool {
	return d.draining.Load() != 0
}
