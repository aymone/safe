package safe_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/aymone/safe"
)

func ExampleCall() {
	err := safe.Call(context.Background(), "http", "handler", func() error {
		panic("user 123: invalid token abc") // sensitive value stays in the log
	})

	var pe *safe.PanicError
	if errors.As(err, &pe) {
		fmt.Println(pe.Error()) // safe for clients: no value, no stack
	}
	// Output: panic in handler
}

func ExampleGo() {
	done := make(chan struct{})
	safe.Go(context.Background(), "worker", func(ctx context.Context) {
		defer close(done)
		panic("recovered and logged, process keeps running")
	})
	<-done
	fmt.Println("still alive")
	// Output: still alive
}

func ExampleLoop() {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	ticks := 0
	safe.Loop(ctx, "poller", 10*time.Millisecond, func(ctx context.Context) error {
		ticks++
		return nil
	})
	fmt.Println(ticks > 0)
	// Output: true
}

func ExampleConfigure() {
	safe.Configure(safe.Options{
		Logger: func(ctx context.Context) *slog.Logger {
			return slog.New(slog.NewJSONHandler(os.Stderr, nil))
		},
		OnPanic: func(_ context.Context, pe *safe.PanicError) {
			fmt.Printf("panic at %s/%s\n", pe.Location, pe.Component)
		},
		Allowed: map[string]string{"fetch": "consumer"},
	})
	defer safe.Configure(safe.Options{})

	_ = safe.Call(context.Background(), "consumer", "fetch", func() error { panic("x") })
	_ = safe.Call(context.Background(), "consumer", "typo", func() error { panic("x") })
	// Output:
	// panic at consumer/fetch
	// panic at goroutine/unknown
}
