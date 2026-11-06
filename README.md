# safe

[![Go Reference](https://pkg.go.dev/badge/github.com/aymone/safe.svg)](https://pkg.go.dev/github.com/aymone/safe)
[![CI](https://github.com/aymone/safe/actions/workflows/ci.yml/badge.svg)](https://github.com/aymone/safe/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/github/go-mod/go-version/aymone/safe)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Panic-safe goroutines for Go, with zero dependencies.

A panic inside a goroutine that nobody recovers does not unwind through the
caller's `recover()`: it kills the whole process. `safe` puts that `recover()`
in one place, so every panic becomes one structured `slog` line plus one
optional hook call, and the process keeps running.

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

```go
safe.Go(ctx, "mailer", func(ctx context.Context) {
    sendEmails(ctx) // a panic here is logged, the process survives
})

err := safe.Call(ctx, "http", "checkout", func() error {
    return handle(req)
})
if safe.IsPanic(err) {
    http.Error(w, err.Error(), http.StatusInternalServerError) // "panic in checkout"
}
```

`PanicError.Error()` never includes the panic value or the stack (both may hold
sensitive data and only go to the log), so it is safe to return to a client.

`location` and `component` are free-form labels you choose: `location` is
*where* it ran (`http`, `consumer`, ...; `Go` uses `goroutine`, `Loop` uses
`worker`), `component` is *what* ran (`checkout`, `mailer`, ...). They appear in
the log line and in the `PanicError` passed to your hook.

## Configure (optional, once at startup)

Zero config works: logs go to `slog.Default()`, no hook, any label accepted.

```go
safe.Configure(safe.Options{
    // Logger for this ctx, so the panic log keeps your request attributes.
    Logger: func(ctx context.Context) *slog.Logger { return mylog.From(ctx) },

    // Called once per recovered panic, after it is logged:
    // alerting, error tracking, metrics... anything.
    OnPanic: func(ctx context.Context, pe *safe.PanicError) {
        alert.Notify(ctx, pe.Location, pe.Component, pe.Value)
    },

    // Optional closed set of component -> location. Unknown pairs are
    // recorded as goroutine/unknown, so a typo never creates a new label.
    Allowed: map[string]string{"checkout": "http", "mailer": safe.LocationGoroutine},
})
```

## Metrics

`safe` has no metrics dependency; wire `OnPanic` to whatever you use.

<details>
<summary>Prometheus</summary>

```go
panics := promauto.NewCounterVec(prometheus.CounterOpts{
    Name: "myapp_panics_recovered_total",
}, []string{"location", "component"})

safe.Configure(safe.Options{
    Allowed: allowed,
    OnPanic: func(_ context.Context, pe *safe.PanicError) {
        panics.WithLabelValues(pe.Location, pe.Component).Inc()
    },
})

// Optional: pre-register series so rate() is 0, not empty, before the first panic.
for component, location := range allowed {
    panics.WithLabelValues(location, component).Add(0)
}
panics.WithLabelValues(safe.UnknownLocation, safe.UnknownComponent).Add(0)
```

</details>

<details>
<summary>OpenTelemetry</summary>

```go
counter, _ := otel.Meter("myapp").Int64Counter("panics.recovered")

safe.Configure(safe.Options{
    OnPanic: func(ctx context.Context, pe *safe.PanicError) {
        counter.Add(ctx, 1, metric.WithAttributes(
            attribute.String("location", pe.Location),
            attribute.String("component", pe.Component),
        ))
    },
})
```

</details>

Using `Allowed` keeps metric label cardinality bounded.

## Forbid bare `go` statements

```go
func TestNoBareGo(t *testing.T) { safetest.NoBareGo(t) }
```

Fails if any non-test `.go` file in the module has a `go` statement, so every
goroutine goes through `safe`. Pass directories (relative to the module root)
that may still use `go`: `safetest.NoBareGo(t, "internal/legacy")`.
`_test.go`, `vendor`, `testdata` and hidden directories are ignored.

## License

[MIT](LICENSE)
