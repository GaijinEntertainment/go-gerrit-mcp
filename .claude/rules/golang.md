---
paths:
  - "**/*.go"
---

# Go Conventions

Required reading: [Effective Go](https://go.dev/doc/effective_go) and the
[Google Go Style Guide](https://google.github.io/styleguide/go/) — especially
[Style Decisions](https://google.github.io/styleguide/go/decisions).

## Error handling — golib/e (mandatory)

Use [`dev.gaijin.team/go/golib/e`](https://pkg.go.dev/dev.gaijin.team/go/golib/e) consistently instead of
`fmt.Errorf`/`errors.New` for creating and wrapping errors.

- Sentinels are objects: `var ErrNotFound = e.New("not found")`; wrap the cause via `ErrNotFound.Wrap(err)`.
- New error wrapping a cause: `e.NewFrom("create service", err)`; convert foreign errors: `e.From(err)`.
- Structured context goes into fields, not the message: `e.NewFrom("query failed", err, fields.F("change", num))`
  or chained `.WithField(k, v)`; snake_case keys.
- Render format is `<reason> (fields...): <wrapped>`. Keep reason strings stable, lowercase, no trailing
  punctuation, no "failed to" prefix.
- Match with `errors.Is`/`errors.As`, never `==`.
- Handle once: log or return, never both. Logging integration: `e.Log(err, logger.Error)`.
- `errors.Join` remains the tool for aggregating independent errors; each joined error is still built with `e`.

## golib/must

[`dev.gaijin.team/go/golib/must`](https://pkg.go.dev/dev.gaijin.team/go/golib/must) only where failure is a
programming bug: package initialization, static data (URLs, regexes). Never on user input, network, or runtime paths.

## Testing

- testify only: `assert` (non-fatal), `require` (fatal), `suite` when a test group needs shared lifecycle.
- Black-box preferred: `*_test.go` with `package foo_test`; `*_internal_test.go` with `package foo` is a last
  resort. Benchmarks: `*_benchmark_test.go` / `*_benchmark_internal_test.go`.
- Never write to the source directory or mutate process env in tests: `t.TempDir()`, `t.Setenv()`.
- Prefer live services over synthetic mocks where practical; gate slow/external tests behind env vars and skip
  when unset.

## Documentation and naming

- Every exported symbol gets a godoc comment starting with its name; `doc.go` for package-level docs; runnable
  `func Example...` for complex APIs.
- Long-running processes: `Run` blocks until completion; `Start` returns immediately, spawns the goroutine, and
  takes `context.Context` as its first parameter.
