# safe

Panic-safe goroutines for Go. A panic in a goroutine nobody recovers kills the
whole process; `safe` puts the `recover()` in one place so every panic becomes
one structured log line plus one hook call (e.g. a metric), and the process
keeps running.

```sh
go get github.com/aymone/safe
```

## API

| Function | Behavior |
|---|---|
| `Call(ctx, location, component, fn) error` | runs `fn`; a panic becomes `*PanicError` (plain errors pass through untouched) |
| `Go(ctx, component, fn)` | goroutine with recover; panic is recorded, never re-raised |
| `Loop(ctx, component, every, fn)` | ticker loop; panicking or failing ticks are recorded/logged and the loop continues until `ctx` is done |
| `IsPanic(err)` | `errors.As` shortcut for `*PanicError` |

`PanicError.Error()` never includes the stack, so it is safe to return to an
HTTP client or wrap.

## Configure (optional, once at startup)

```go
panics := promauto.NewCounterVec(prometheus.CounterOpts{
    Name: "myapp_panics_recovered_total",
}, []string{"location", "component"})

allowed := map[string]string{ // component -> location
    "http":   "http",
    "fetch":  "consumer",
    "relay":  safe.LocationWorker,
    "server": safe.LocationGoroutine,
}

safe.Configure(safe.Options{
    Logger:  func(ctx context.Context) *slog.Logger { return mylog.From(ctx) },
    OnPanic: func(_ context.Context, pe *safe.PanicError) {
        panics.WithLabelValues(pe.Location, pe.Component).Inc()
    },
    Allowed: allowed, // nil = accept any pair
})

// Pre-register series so rate() is 0, not empty, before the first panic.
for c, l := range allowed {
    panics.WithLabelValues(l, c).Add(0)
}
panics.WithLabelValues(safe.UnknownLocation, safe.UnknownComponent).Add(0)
```

Zero config works: logs go to `slog.Default()`, no hook, any pair accepted.
With `Allowed` set, an unregistered pair is recorded as
`goroutine/unknown`, so a typo can never mint a new metric series.

## Forbid bare `go` statements

```go
func TestNoBareGo(t *testing.T) { safetest.NoBareGo(t) }
```

Fails if any non-test `.go` file in the module has a `go` statement.
Pass directories (relative to the module root) that are allowed to use `go`:
`safetest.NoBareGo(t, "internal/legacy")`. `_test.go`, `vendor`, `testdata` and
hidden directories are ignored.

## License

MIT
