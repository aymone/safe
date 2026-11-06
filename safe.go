// Package safe starts goroutines and runs functions so that a panic can never
// crash the process silently.
//
// A panic inside a goroutine that nobody recovers does not unwind through the
// caller's recover(): it kills the whole process. Call, Go and Loop put that
// recover in one place, so every panic leaves the same log line and the same
// OnPanic hook call behind, wherever it came from.
//
// Pair it with safetest.NoBareGo to forbid bare `go` statements in a module.
package safe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync/atomic"
	"time"
)

// Locations used by Go and Loop.
const (
	LocationGoroutine = "goroutine"
	LocationWorker    = "worker"
)

// Fallback pair recorded when Options.Allowed is set and a
// (location, component) pair is not in it.
const (
	UnknownLocation  = LocationGoroutine
	UnknownComponent = "unknown"
)

// Options configures the package. The zero value is usable: it logs to
// slog.Default(), calls no hook and accepts any (location, component) pair.
type Options struct {
	// Logger returns the logger for ctx, so panic logs carry whatever
	// attributes the caller attached (request id, tenant, ...).
	// Nil means slog.Default().
	Logger func(ctx context.Context) *slog.Logger

	// OnPanic is called once per recovered panic, after it is logged.
	// Typical use: increment a metric labeled by pe.Location/pe.Component.
	// It must not panic.
	OnPanic func(ctx context.Context, pe *PanicError)

	// Allowed is the closed set of component -> location pairs. When non-nil,
	// any other pair is logged and recorded as UnknownLocation/UnknownComponent,
	// so a typo in a caller can never mint a new metric series.
	// Nil accepts every pair as-is.
	Allowed map[string]string
}

var opts atomic.Pointer[Options]

// Configure sets the package options. Call it once at startup, before any
// Call, Go or Loop; the map in o.Allowed must not be modified afterwards.
func Configure(o Options) { opts.Store(&o) }

func current() *Options {
	if o := opts.Load(); o != nil {
		return o
	}
	return &Options{}
}

func (o *Options) logger(ctx context.Context) *slog.Logger {
	if o.Logger != nil {
		if l := o.Logger(ctx); l != nil {
			return l
		}
	}
	return slog.Default()
}

// PanicError is returned by Call when fn panics. Value and Stack exist for
// server-side logging only (they may hold sensitive data); Error() includes
// neither, so it is safe to surface to a client or wrap into another error.
type PanicError struct {
	Value     any
	Stack     []byte
	Location  string
	Component string
}

func (e *PanicError) Error() string {
	return "panic in " + e.Component
}

// IsPanic reports whether err is or wraps a *PanicError.
func IsPanic(err error) bool {
	var pe *PanicError
	return errors.As(err, &pe)
}

func (o *Options) validate(ctx context.Context, location, component string) (string, string) {
	if o.Allowed == nil {
		return location, component
	}
	if want, ok := o.Allowed[component]; ok && want == location {
		return location, component
	}
	o.logger(ctx).Error("safe: unrecognized location/component pair, recording as fallback",
		"location", location, "component", component,
		"fallbackLocation", UnknownLocation, "fallbackComponent", UnknownComponent)
	return UnknownLocation, UnknownComponent
}

// recordPanic does not call recover(): recover() only works when called
// directly by a deferred function, so each caller recovers in its own defer
// and passes the value in.
func recordPanic(ctx context.Context, location, component string, value any) *PanicError {
	o := current()
	location, component = o.validate(ctx, location, component)
	stack := debug.Stack()
	o.logger(ctx).Error("panic recovered",
		"location", location,
		"component", component,
		"panic", fmt.Sprint(value),
		"panicType", fmt.Sprintf("%T", value),
		"stack", string(stack),
	)
	pe := &PanicError{Value: value, Stack: stack, Location: location, Component: component}
	if o.OnPanic != nil {
		o.OnPanic(ctx, pe)
	}
	return pe
}

// Call runs fn and converts a panic into a *PanicError. A plain error from fn
// is returned unchanged: Call never logs or reports an ordinary failure.
func Call(ctx context.Context, location, component string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = recordPanic(ctx, location, component, r)
		}
	}()
	return fn()
}

// Go starts fn in a goroutine. A panic inside it is recovered and recorded
// with location LocationGoroutine; it is never re-raised.
func Go(ctx context.Context, component string, fn func(ctx context.Context)) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				_ = recordPanic(ctx, LocationGoroutine, component, r)
			}
		}()
		fn(ctx)
	}()
}

// Loop calls fn every `every` until ctx is done, blocking the caller. Each
// tick runs through Call(ctx, LocationWorker, component, ...): a panicking
// tick is recovered and recorded; a tick returning an error is logged but not
// counted as a panic. Either way the loop continues. Loop returns within one
// tick after ctx is canceled.
func Loop(ctx context.Context, component string, every time.Duration, fn func(ctx context.Context) error) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runTick(ctx, component, fn)
		}
	}
}

func runTick(ctx context.Context, component string, fn func(ctx context.Context) error) {
	err := Call(ctx, LocationWorker, component, func() error { return fn(ctx) })
	if err == nil || IsPanic(err) {
		return // a panic was already logged and reported by recordPanic
	}
	current().logger(ctx).Error("worker tick failed", "component", component, "error", err.Error())
}
