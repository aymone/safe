package safe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recorder struct {
	mu  sync.Mutex
	got []*PanicError
}

func (r *recorder) hook(_ context.Context, pe *PanicError) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, pe)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

type ctxKey struct{}

// setup configures the package for one test and restores the zero options
// afterwards. Tests using it must not run in parallel.
func setup(t *testing.T, allowed map[string]string, w io.Writer) *recorder {
	t.Helper()
	rec := &recorder{}
	base := slog.New(slog.NewJSONHandler(w, nil))
	Configure(Options{
		Logger: func(ctx context.Context) *slog.Logger {
			if l, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
				return l
			}
			return base
		},
		OnPanic: rec.hook,
		Allowed: allowed,
	})
	t.Cleanup(func() { Configure(Options{}) })
	return rec
}

func TestCallRecoversPanicWithContext(t *testing.T) {
	var buf strings.Builder
	rec := setup(t, nil, io.Discard)
	ctx := context.WithValue(context.Background(), ctxKey{},
		slog.New(slog.NewJSONHandler(&buf, nil)).With("requestId", "req_1"))

	err := Call(ctx, "consumer", "fetch", func() error { panic("kaboom") })

	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("Call() error = %v, want *PanicError", err)
	}
	if pe.Location != "consumer" || pe.Component != "fetch" || pe.Value != "kaboom" {
		t.Fatalf("PanicError = %s/%s/%v, want consumer/fetch/kaboom", pe.Location, pe.Component, pe.Value)
	}
	if len(pe.Stack) == 0 {
		t.Fatal("PanicError.Stack is empty")
	}
	if got := pe.Error(); got != "panic in fetch" {
		t.Fatalf("PanicError.Error() = %q, want %q (no value, no stack)", got, "panic in fetch")
	}
	if rec.count() != 1 || rec.got[0] != pe {
		t.Fatalf("OnPanic calls = %d, want 1 with the returned error", rec.count())
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d log lines, want 1:\n%s", len(lines), buf.String())
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"stack", "panicType", "component", "location", "requestId"} {
		if _, ok := entry[key]; !ok {
			t.Errorf("log line missing %q: %v", key, entry)
		}
	}
}

func TestCallUnknownPairFallsBack(t *testing.T) {
	rec := setup(t, map[string]string{"fetch": "consumer"}, io.Discard)

	err := Call(context.Background(), "consumer", "not_a_real_component", func() error { panic("boom") })

	var pe *PanicError
	if !errors.As(err, &pe) {
		t.Fatalf("Call() error = %v, want *PanicError", err)
	}
	if pe.Location != UnknownLocation || pe.Component != UnknownComponent {
		t.Fatalf("fallback = %s/%s, want %s/%s", pe.Location, pe.Component, UnknownLocation, UnknownComponent)
	}
	if rec.count() != 1 {
		t.Fatalf("OnPanic calls = %d, want 1", rec.count())
	}
}

func TestCallNilAllowedAcceptsAnyPair(t *testing.T) {
	setup(t, nil, io.Discard)
	err := Call(context.Background(), "x", "y", func() error { panic("boom") })
	var pe *PanicError
	if !errors.As(err, &pe) || pe.Location != "x" || pe.Component != "y" {
		t.Fatalf("Call() = %v, want PanicError x/y", err)
	}
}

func TestCallReturnsErrorUnchanged(t *testing.T) {
	rec := setup(t, nil, io.Discard)
	want := errors.New("boom")

	if err := Call(context.Background(), "consumer", "fetch", func() error { return want }); err != want {
		t.Fatalf("Call() error = %v, want %v", err, want)
	}
	if rec.count() != 0 {
		t.Fatalf("OnPanic called on plain error")
	}
}

func TestZeroConfigWorks(t *testing.T) {
	Configure(Options{})
	if err := Call(context.Background(), "a", "b", func() error { panic(42) }); !IsPanic(err) {
		t.Fatalf("Call() = %v, want panic error", err)
	}
}

func TestGoRecoversPanicAndSurvives(t *testing.T) {
	rec := setup(t, nil, io.Discard)

	Go(context.Background(), "receive", func(context.Context) { panic("boom") })

	waitFor(t, func() bool { return rec.count() == 1 })
	if pe := rec.got[0]; pe.Location != LocationGoroutine || pe.Component != "receive" {
		t.Fatalf("PanicError = %s/%s, want goroutine/receive", pe.Location, pe.Component)
	}
}

func TestLoopContinuesAfterPanicAndStopsOnCancel(t *testing.T) {
	rec := setup(t, nil, io.Discard)
	ctx, cancel := context.WithCancel(context.Background())

	var ticks atomic.Int32
	done := make(chan struct{})
	go func() {
		Loop(ctx, "relay", 5*time.Millisecond, func(context.Context) error {
			switch ticks.Add(1) {
			case 1:
				panic("boom")
			case 2:
				return errors.New("plain")
			}
			return nil
		})
		close(done)
	}()

	waitFor(t, func() bool { return ticks.Load() >= 3 })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Loop did not stop after ctx cancel")
	}
	if rec.count() != 1 || rec.got[0].Location != LocationWorker {
		t.Fatalf("OnPanic calls = %d, want 1 worker panic", rec.count())
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
