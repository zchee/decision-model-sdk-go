# typesafe-sdk-go

typesafe-sdk-go is a Go client for the [TypeSafe](https://typesafe.ai)
System One API, a port of
[typesafe-sdk-python](https://github.com/typesafe-ai/typesafe-sdk-python)
0.7.1. It asks questions about a state (a support ticket, a review, any
JSON) and returns typed answers: a probability (noul), a choice among
options, or a score on a scale.

**Status: pre-release.** No version is tagged yet (`typesafe.Version` is
`0.1.0-dev`); the API may change before v0.1.0.

## Install

```sh
go get github.com/zchee/typesafe-sdk-go
```

- The SDK supports Go 1.27.x on `amd64` and `arm64`, the releases the newest
  `github.com/bytedance/sonic` tag supports. A `go` command from Go 1.21 to
  1.26 switches to a Go 1.27 toolchain through the `go.mod` line (with
  `GOTOOLCHAIN=auto`, the default); Go 1.17 to 1.20 attempt the build and
  print `note: module requires Go 1.27` when it fails. On any other GOARCH,
  or on Go 1.28 and later, the build fails on purpose with the error
  `undefined: typesafe_sdk_go_requires_go1_17_to_go1_27_on_amd64_or_arm64`
  ([`docs/support.md`](docs/support.md) explains why and holds the Go 1.28
  bump procedure).

## Quick start

Set `TYPESAFE_API_KEY` in your environment, then build a client and ask a
question. `NewClient` without options reads the key from
`TYPESAFE_API_KEY` and, when set, the API's address from
`TYPESAFE_BASE_URL`.

<!-- example: quickstart/main.go -->
```go
// Copyright 2026 The typesafe-sdk-go Authors.
// SPDX-License-Identifier: Apache-2.0

// Command quickstart asks the TypeSafe API one question about a support
// ticket and prints the answer. The client reads the API key from the
// TYPESAFE_API_KEY environment variable.
package main

import (
	"context"
	"fmt"
	"log"

	typesafe "github.com/zchee/typesafe-sdk-go"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	client, err := typesafe.NewClient()
	if err != nil {
		return err
	}
	defer client.Close()

	questions, err := typesafe.NewQuestions().
		Choice("category", typesafe.Choice{
			Instructions: typesafe.Text("What is this ticket about?"),
			Options:      typesafe.Options{{Label: "billing"}, {Label: "technical"}, {Label: "other"}},
		}).
		Prepare()
	if err != nil {
		return err
	}

	state := map[string]any{"document": "I was charged twice. Please fix this ASAP."}
	response, err := client.SystemOne(ctx, state, questions)
	if err != nil {
		return err
	}

	category, _ := response.Answers().Choice("category")
	fmt.Println(category.Choice())
	return nil
}
```

Run it with `go run ./examples/quickstart` from a checkout of this
repository.

## Typed answers

A struct can declare the questions in its field tags and receive the
answers in its fields. `Ask[T]` sends the questions `T` declares and
decodes the answers into a `T`; `PreparedFor[T]` and `DecodeAs[T]` are
the two halves, for a caller that also wants the response (its request
id, usage or raw body). The tag's `name` is the question's name on the
wire; without it the name is the Go field name as written.

<!-- example: typed/main.go -->
```go
// Copyright 2026 The typesafe-sdk-go Authors.
// SPDX-License-Identifier: Apache-2.0

// Command typed declares its questions as a struct: each tagged field is a
// question, and receives its answer.
package main

import (
	"context"
	"fmt"
	"log"

	typesafe "github.com/zchee/typesafe-sdk-go"
)

// Ticket is the question set. The tag's name key is the question's name on
// the wire (the field name when left out); optional marks an answer that
// may be missing from the response, which Present then reports.
type Ticket struct {
	Billing typesafe.NoulAnswer   `typesafe:"kind=noul;name=billing;instructions=Is this ticket about billing?"`
	Tone    typesafe.ChoiceAnswer `typesafe:"kind=choice;name=tone;instructions=What is the customer's tone?;options=calm|frustrated|angry"`
	Urgency typesafe.ScoreAnswer  `typesafe:"kind=score;name=urgency;instructions=How urgent is this ticket?;levels=can wait|this week|today"`
	Spam    typesafe.NoulAnswer   `typesafe:"kind=noul;name=spam;optional;instructions=Is this spam?"`
}

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	client, err := typesafe.NewClient()
	if err != nil {
		return err
	}
	defer client.Close()

	state := map[string]any{
		"subject": "Charged twice this month",
		"body":    "I see two charges of $49. Please fix this ASAP.",
	}

	// One step: Ask sends the questions Ticket declares and returns the
	// answers as a Ticket.
	ticket, err := typesafe.Ask[Ticket](ctx, client, state)
	if err != nil {
		return err
	}
	fmt.Printf("billing: %.2f\n", ticket.Billing.Noul())
	fmt.Printf("tone: %s (confidence %.2f)\n", ticket.Tone.Choice(), ticket.Tone.Confidence())
	fmt.Printf("urgency: %.2f\n", ticket.Urgency.Score())
	if ticket.Spam.Present() {
		fmt.Printf("spam: %.2f\n", ticket.Spam.Noul())
	}

	// Two steps, when the response itself is needed too (its request id,
	// usage or raw body): the questions Ask would send, the call, and the
	// typed decode of its answers.
	questions, err := typesafe.PreparedFor[Ticket]()
	if err != nil {
		return err
	}
	response, err := client.SystemOne(ctx, state, questions)
	if err != nil {
		return err
	}
	again, err := typesafe.DecodeAs[Ticket](response)
	if err != nil {
		return err
	}
	id, _ := response.Meta().RequestID()
	fmt.Printf("request %s: tone %s\n", id, again.Tone.Choice())
	return nil
}
```

## Retries and errors

A `RetryPolicy` is a value whose zero value is `DefaultRetry`, the Python
SDK's `RetryPolicy()`: 2 retries on 408, 429 and 5xx, on connection errors
and on timeouts, with a backoff from 500 ms to 5 s and a budget of 30 s per
call. `WithRetry` sets a client's policy and `Retry` one call's. A
successful response is billed, so a 2xx is never retried by its status.

Errors are pointers: `*APIError`, `*ConnectionError`, `*TimeoutError`,
`*ResponseValidationError`, `*ResponseTooLargeError`, `*ConfigError` and
`*InvalidRequestError`, each an `Error`. Ask for them with
`errors.AsType[*typesafe.APIError](err)`, or with `errors.As` and a
`**typesafe.APIError` (`var e *typesafe.APIError; errors.As(err, &e)`);
`errors.As` panics on a value target (`var e typesafe.APIError`), since
only the pointer type is an error. A cancelled context returns
`context.Canceled` itself.

<!-- example: retries/main.go -->
```go
// Copyright 2026 The typesafe-sdk-go Authors.
// SPDX-License-Identifier: Apache-2.0

// Command retries sets a retry policy for a client and for one call, and
// tells the errors of a call apart once its retries are spent.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	typesafe "github.com/zchee/typesafe-sdk-go"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	// A policy is a value; each setter returns a changed copy. The zero
	// policy is DefaultRetry: 2 retries on 408, 429 and 5xx, on connection
	// errors and on timeouts, with a backoff from 500 ms to 5 s and a
	// budget of 30 s per call.
	patient := typesafe.DefaultRetry().
		MaxRetries(4).
		Backoff(time.Second, 10*time.Second, 0.25).
		Budget(time.Minute)
	client, err := typesafe.NewClient(typesafe.WithRetry(patient))
	if err != nil {
		return err
	}
	defer client.Close()

	questions, err := typesafe.NewQuestions().
		Noul("refund", typesafe.Noul{Instructions: typesafe.Text("Does the customer ask for a refund?")}).
		Prepare()
	if err != nil {
		return err
	}
	state := "I was charged twice; please refund one of the charges."

	// This call retries nothing; the client's policy applies to the others.
	response, err := client.SystemOne(ctx, state, questions, typesafe.Retry(typesafe.NoRetry()))
	report("without retries", response, err)

	// A successful response is billed, so one that fails validation is
	// never retried unless a predicate asks for it.
	validating := patient.Predicate(func(err error) bool {
		_, ok := errors.AsType[*typesafe.ResponseValidationError](err)
		return ok
	})
	response, err = client.SystemOne(ctx, state, questions, typesafe.Retry(validating))
	report("with a predicate", response, err)
	return nil
}

// report prints the answer, or what kind of error the call returned.
func report(label string, response *typesafe.SystemOneResponse, err error) {
	if err == nil {
		refund, _ := response.Answers().Noul("refund")
		fmt.Printf("%s: refund %.2f\n", label, refund.Noul())
		return
	}
	if apiErr, ok := errors.AsType[*typesafe.APIError](err); ok {
		wait, _ := apiErr.RetryAfter()
		fmt.Printf("%s: the API answered %d (%s, authentication %t, retry after %s)\n",
			label, apiErr.StatusCode, apiErr.Kind, apiErr.IsAuthentication(), wait)
		return
	}
	if timeoutErr, ok := errors.AsType[*typesafe.TimeoutError](err); ok {
		fmt.Printf("%s: no response within %s\n", label, timeoutErr.Timeout)
		return
	}
	if connErr, ok := errors.AsType[*typesafe.ConnectionError](err); ok {
		fmt.Printf("%s: no connection (through a proxy: %t): %v\n", label, connErr.Proxy(), err)
		return
	}
	if errors.Is(err, context.Canceled) {
		fmt.Printf("%s: cancelled\n", label)
		return
	}
	fmt.Printf("%s: %v\n", label, err)
}
```

## Logging and redaction

The client writes to the `log/slog` logger given by `WithLogger`, and
discards its records without one. INFO holds one record per attempt,
DEBUG adds the headers and the body lengths, and `typesafe.LevelTrace`
adds the bodies.

<!-- example: logging/main.go -->
```go
// Copyright 2026 The typesafe-sdk-go Authors.
// SPDX-License-Identifier: Apache-2.0

// Command logging gives the client a log/slog logger. At INFO the client
// writes one record per attempt; at DEBUG it adds the request and response
// headers, with credentials shown as ***, and the body lengths; at
// typesafe.LevelTrace it adds the bodies themselves, as sent and received.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"

	typesafe "github.com/zchee/typesafe-sdk-go"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client, err := typesafe.NewClient(typesafe.WithLogger(logger))
	if err != nil {
		return err
	}
	defer client.Close()

	questions, err := typesafe.NewQuestions().
		Noul("greeting", typesafe.Noul{Instructions: typesafe.Text("Is this a greeting?")}).
		Prepare()
	if err != nil {
		return err
	}
	response, err := client.SystemOne(ctx, "Hello there!", questions)
	if err != nil {
		return err
	}
	greeting, _ := response.Answers().Noul("greeting")
	fmt.Printf("greeting: %.2f\n", greeting.Noul())
	return nil
}
```

What the SDK redacts, in its errors and in its log records alike:

- **Header-derived values are redacted.** The value of a credential
  header (`Authorization`, `Proxy-Authorization`, `X-Api-Key`, `Api-Key`,
  `Cookie`, `Set-Cookie`, or a name that contains `token` or `secret`), and
  any header value that holds the client's API key (a key of at least 8
  bytes), is shown as `***`. This covers the request headers of the DEBUG
  records, the response header an `*APIError`,
  `*ResponseValidationError` or `*ResponseTooLargeError` keeps, and the
  request id, in `RequestID()`, in `Error()` and in the INFO record.
- **Body-derived text is shown as received.** A server's error message, a
  field path and the name of a skipped answer at WARN are the server's
  text, as the Python SDK shows them: a key the server echoes there is
  visible. They are escaped and cut (messages at 200 characters, names and
  request ids at 128, field paths at 320).
- **`LevelTrace` records are the bodies.** The request and response
  bodies are logged as they are; enable that level only where bodies may
  be logged.

A transport error's text shows its credentials as `***`; when the text of
its cause printed a credential, a stand-in holding the redacted text takes
the cause's place.

## More examples

[`docs/examples.md`](docs/examples.md) shows every program under
[`examples/`](examples): the four above, and the client and call options,
custom transports, and one client shared by goroutines. Each Go block in
this README and in `docs/` is one of those programs, byte for byte, and CI
checks that it is.

## Differences from the Python SDK

The port keeps the Python SDK's behaviour except where Go, or a ruling
made during the port, says otherwise. The main differences:

- One `*Client` for synchronous and concurrent use; every call takes a
  `context.Context`, and each attempt has one deadline.
- On an https base URL the client speaks HTTP/2 only, on one connection,
  and refuses an API server that does not negotiate h2
  (`ErrHTTP2NotNegotiated`); `WithHTTPVersion(HTTPAuto)` lets ALPN choose.
- Typed answers come from struct tags (`Ask[T]`) instead of pydantic
  models. The typed decode refuses what the question set does not declare
  (an unknown option label, a level beyond the levels) and requires
  `usage`; its error paths keep the Python SDK's form (`tone.choice`). It
  writes the answers at their fields' offsets through `unsafe`, in one
  file of the package, so that it does not allocate.
- The error types keep a redacted copy of the response header, and the
  request id is redacted in the INFO record too (see above).
- `RetryPolicy` holds `time.Duration` values, and the budget counts from
  the first attempt; a deadline that ends a wait returns a `*TimeoutError`.
- A response is read under a 16 MiB limit (`WithMaxResponseBytes`).
- `TYPESAFE_LOG_LEVEL` is not read; bodies are logged at
  `typesafe.LevelTrace` only.

[`docs/deviations.md`](docs/deviations.md) is the full table, each row
naming the Python behaviour, the Go behaviour and why, and the upstream
tests it replaces; [`docs/port-test-matrix.md`](docs/port-test-matrix.md)
maps every one of the Python SDK's 129 tests to a Go test or to a row of
that table.

## Tests

`go test ./...` runs every test offline, the examples included (against
a local stand-in for the API). The tests against the live API are opt-in:
they compile only with the build tag `live`, and each fails before it
calls the API unless both `TYPESAFE_LIVE_TESTS=1` and `TYPESAFE_API_KEY`
are set. With the key already in your environment:

```sh
TYPESAFE_LIVE_TESTS=1 go test -tags live -count=1 -v ./livetests/
```

The key is read from the environment and is never printed. The API bills
each System One call; CI does not run these tests
([`docs/support.md`](docs/support.md#live-tests) says where they run).

## CI, coverage and benchmarks

- [CI](.github/workflows/ci.yaml) runs the linters (gofumpt, modernize,
  golangci-lint, vet, staticcheck, govulncheck, `go mod tidy -diff`), the
  import-confinement tests, the port test matrix, the docs-snippets check,
  `go vet` of the examples, and the compile-time refusal off the support
  matrix; then the tests with `-race` on `ubuntu-26.04`, `xcode-27` and
  `windows-2025`, and the allocation budgets without it.
- [Codecov](https://codecov.io/gh/zchee/typesafe-sdk-go) receives each
  image's coverage; its project status blocks at the 85 % target (the
  90 % goal and the patch status are informational), and every block the
  tests leave unrun has a reason in
  [`docs/uncovered-lines.md`](docs/uncovered-lines.md).
- [CodSpeed](https://codspeed.io/zchee/typesafe-sdk-go) runs every
  benchmark in walltime mode on `ubuntu-26.04`. The comparison of a whole
  call against a naive sonic client (AC-P7) is reported, not enforced
  ([`docs/perf/codspeed.md`](docs/perf/codspeed.md)).

## License

Licensed under the Apache License, Version 2.0 ([LICENSE](LICENSE)).
Third-party licence texts are available from each module's source in the Go
module cache (`go mod download -json <module>` gives the directory).
