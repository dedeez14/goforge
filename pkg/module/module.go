// Package module defines the contract every goforge module must
// implement and a Registry that wires modules into the running
// application.
//
// A goforge module is a self-contained, opt-in feature pack: it can
// register HTTP routes, expose embedded SQL migrations, run background
// workers, subscribe to the in-process event bus, and report its own
// health. Modules are the unit of reuse and the integration point for
// the wider ecosystem - third parties can publish modules that work
// inside any goforge application without touching the core.
package module

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"sync"

	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog"
)

// Module is the contract a goforge module must satisfy. Every method is
// optional except Name(); the default implementations in BaseModule
// keep most modules to a few lines.
type Module interface {
	// Name uniquely identifies the module (e.g. "auth", "audit",
	// "outbox"). Used for ordering, logging and CLI commands.
	Name() string
	// Init lets the module construct internal state, connect to its
	// own infrastructure or read config. It receives the shared
	// application context built in internal/app.
	Init(ctx context.Context, app *Context) error
	// Routes registers HTTP handlers under the application's Fiber
	// router. Modules are free to attach to any group; goforge
	// guarantees the global middleware chain is already in place.
	Routes(r fiber.Router)
	// Migrations returns an optional embedded migrations FS. The
	// migration runner discovers `up` and `down` SQL files using the
	// golang-migrate naming convention.
	Migrations() (fs.FS, string)
	// Workers returns long-running goroutines this module wants the
	// supervisor to run. Each worker is given the application
	// shutdown context and must return when it is cancelled.
	Workers() []Worker
	// Subscriptions lets the module register handlers on the shared
	// in-process event bus.
	Subscriptions(bus EventBus)
	// Health is invoked by /readyz to compose a per-module readiness
	// signal. Returning an error fails readiness without taking
	// liveness down.
	Health(ctx context.Context) error
	// Shutdown is called once on graceful termination. Modules should
	// flush, close connections and release any resources here.
	Shutdown(ctx context.Context) error
}

// Worker is a long-running goroutine spawned by a module. The runner
// passes a context that is cancelled on shutdown - implementations must
// respect it.
type Worker func(ctx context.Context) error

// EventBus is the minimal subset of the events package that modules
// touch when they wire subscriptions. The full bus lives at
// pkg/events; this interface is here to avoid a circular dependency.
type EventBus interface {
	Subscribe(topic string, handler func(ctx context.Context, payload []byte) error)
}

// Context aggregates the shared services an application exposes to its
// modules at Init time. Keeping this in one struct means modules don't
// have to know about every infrastructure package.
type Context struct {
	// Logger is the application zerolog instance, already enriched
	// with the module name.
	Logger zerolog.Logger
	// Bus is the in-process event bus shared by all modules.
	Bus EventBus
	// Values is a typed bag of optional services injected by the
	// composition root (database pool, cache client, secrets, etc.).
	// Modules call Get to retrieve a value by its registered key and
	// fail fast if it is missing.
	Values *Values
}

// Values is a goroutine-safe map keyed by string; the composition root
// populates it once during boot and modules read it during Init.
type Values struct {
	mu sync.RWMutex
	m  map[string]any
}

// NewValues returns an empty Values bag ready for use.
func NewValues() *Values { return &Values{m: make(map[string]any)} }

// Set stores value under key, overwriting any previous binding.
func (v *Values) Set(key string, value any) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.m[key] = value
}

// Get returns the value previously stored under key. The boolean
// reports whether the key was present, mirroring map semantics.
func (v *Values) Get(key string) (any, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	value, ok := v.m[key]
	return value, ok
}

// MustGet is the typed convenience wrapper used by module Init code.
// It panics when the key is missing or the type does not match - both
// outcomes indicate a wiring bug that must be fixed at boot time.
func MustGet[T any](v *Values, key string) T {
	raw, ok := v.Get(key)
	if !ok {
		panic(fmt.Sprintf("module: required value %q not found in context", key))
	}
	value, ok := raw.(T)
	if !ok {
		panic(fmt.Sprintf("module: value %q has unexpected type %T", key, raw))
	}
	return value
}

// Registry tracks every module known to the application. The zero
// value is ready to use; Register and Each are the only operations.
type Registry struct {
	mu      sync.Mutex
	modules []Module
}

// Register adds m to the registry. Module names must be unique;
// registering a duplicate returns ErrDuplicateModule so the caller can
// fail fast at boot time.
func (r *Registry) Register(m Module) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.modules {
		if existing.Name() == m.Name() {
			return fmt.Errorf("%w: %q", ErrDuplicateModule, m.Name())
		}
	}
	r.modules = append(r.modules, m)
	return nil
}

// MustRegister is Register that panics on failure; intended for use in
// the composition root where wiring errors should crash the process.
func (r *Registry) MustRegister(m Module) {
	if err := r.Register(m); err != nil {
		panic(err)
	}
}

// Each returns a snapshot of the registered modules sorted by name so
// boot order is deterministic. Callers must not mutate the slice.
func (r *Registry) Each() []Module {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Module, len(r.modules))
	copy(out, r.modules)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Names returns the registered module names in deterministic order;
// useful for `forge module list` and diagnostic output.
func (r *Registry) Names() []string {
	modules := r.Each()
	names := make([]string, len(modules))
	for i, m := range modules {
		names[i] = m.Name()
	}
	return names
}

// ErrDuplicateModule is returned by Register when a module with the
// same name has already been added.
var ErrDuplicateModule = errors.New("module already registered")
