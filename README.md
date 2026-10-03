# decision-model-sdk-go

decision-model-sdk-go is a Go client for decision models served through the
System One API: the System One endpoint (`POST /v1/systemone`), which
answers named questions about a state, and the model listing
(`GET /v1/models`). The API and its decision models were first offered by
[TypeSafe AI](https://typesafe.ai) (Jev), and other vendors serve them too;
a client talks to the vendor whose base URL it is given, and the SDK has no
default one. The SDK is a port of TypeSafe AI's Python SDK,
[typesafe-sdk-python](https://github.com/typesafe-ai/typesafe-sdk-python)
0.7.1. It asks questions about a state (a support ticket, a review, any
JSON) and returns typed answers: a probability (noul), a choice among
options, or a score on a scale.

**Status: v0.1.0**, the first release under this module path
(`decision.Version` is `0.1.0`). Before v1.0.0 a minor release may change
the API, as
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) allows.

## Install

```sh
go get github.com/zchee/decision-model-sdk-go
```

- The SDK supports Go 1.27.x on `amd64` and `arm64`, the releases the newest
  `github.com/bytedance/sonic` tag supports. Its `go.mod` says `go 1.27`, so
  with `GOTOOLCHAIN=auto`, the default, `go get` from Go 1.21 to 1.26
  switches to the newest Go 1.27 release and writes `go 1.27` into your
  `go.mod`. Every `go` command after it in your module, as in a checkout of
  this repository, then switches to `go1.27.0`, except that Go 1.21.0 to
  1.21.10 and 1.22.0 to 1.22.3 stop with `toolchain not available`. Go 1.17
  to 1.20 fail on the standard-library packages they lack. On any other
  GOARCH, or on Go 1.28 and later, the build fails on purpose with the error
  `undefined: decision_model_sdk_go_requires_go1_17_to_go1_27_on_amd64_or_arm64`
  ([`docs/support.md`](docs/support.md) gives each case, explains why, and
  holds the Go 1.28 bump procedure).

## Quick start

Set `DECISION_MODEL_API_KEY`, `DECISION_MODEL_BASE_URL` and
`DECISION_MODEL_DEFAULT_MODEL` in your environment, then build a client and
ask a question. `NewClient` without options reads the API key, the API's
base URL and the default model from those variables; for TypeSafe AI's API
the base URL is `https://api.typesafe.ai`, and `jev-latest` is one of its
models. The SDK has no default base URL or model, since the API is served by
more than one vendor: without a base URL `NewClient` fails, and without a
model each call names its own with `Model`.

<!-- example: quickstart/main.go -->
```go
// Copyright 2026 The decision-model-sdk-go Authors.
// SPDX-License-Identifier: Apache-2.0

// Command quickstart asks a decision model one question about a support
// ticket and prints the answer. The client reads the API key, the API's base
// URL and the model from the DECISION_MODEL_API_KEY, DECISION_MODEL_BASE_URL
// and DECISION_MODEL_DEFAULT_MODEL environment variables.
package main

import (
	"context"
	"fmt"
	"log"

	decision "github.com/zchee/decision-model-sdk-go"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	client, err := decision.NewClient()
	if err != nil {
		return err
	}
	defer client.Close()

	questions, err := decision.NewQuestions().
		Choice("category", decision.Choice{
			Instructions: decision.Text("What is this ticket about?"),
			Options:      decision.Options{{Label: "billing"}, {Label: "technical"}, {Label: "other"}},
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
// Copyright 2026 The decision-model-sdk-go Authors.
// SPDX-License-Identifier: Apache-2.0

// Command typed declares its questions as a struct: each tagged field is a
// question, and receives its answer.
package main

import (
	"context"
	"fmt"
	"log"

	decision "github.com/zchee/decision-model-sdk-go"
)

// Ticket is the question set. The tag's name key is the question's name on
// the wire (the field name when left out); optional marks an answer that
// may be missing from the response, which Present then reports.
type Ticket struct {
	Billing decision.NoulAnswer   `decision:"kind=noul;name=billing;instructions=Is this ticket about billing?"`
	Tone    decision.ChoiceAnswer `decision:"kind=choice;name=tone;instructions=What is the customer's tone?;options=calm|frustrated|angry"`
	Urgency decision.ScoreAnswer  `decision:"kind=score;name=urgency;instructions=How urgent is this ticket?;levels=can wait|this week|today"`
	Spam    decision.NoulAnswer   `decision:"kind=noul;name=spam;optional;instructions=Is this spam?"`
}

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	client, err := decision.NewClient()
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
	ticket, err := decision.Ask[Ticket](ctx, client, state)
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
	questions, err := decision.PreparedFor[Ticket]()
	if err != nil {
		return err
	}
	response, err := client.SystemOne(ctx, state, questions)
	if err != nil {
		return err
	}
	again, err := decision.DecodeAs[Ticket](response)
	if err != nil {
		return err
	}
	id, _ := response.Meta().RequestID()
	fmt.Printf("request %s: tone %s\n", id, again.Tone.Choice())
	return nil
}
```

## Retries and errors

A `RetryPolicy` is a value; its zero value is `DefaultRetry`, the Python
SDK's `RetryPolicy()`. `WithRetry` sets a client's policy and `Retry` one
call's. A successful response is billed, so a 2xx is never retried by its
status.

Errors are pointers: `*APIError`, `*ConnectionError`, `*TimeoutError`,
`*ResponseValidationError`, `*ResponseTooLargeError`, `*ConfigError` and
`*InvalidRequestError`, each an `Error`. Ask for them with
`errors.AsType[*decision.APIError](err)`, or with `errors.As` and a
`**decision.APIError` (`var e *decision.APIError; errors.As(err, &e)`);
`errors.As` panics on a value target (`var e decision.APIError`), since
only the pointer type is an error. A cancelled context returns
`context.Canceled` itself.

<!-- example: retries/main.go -->
```go
// Copyright 2026 The decision-model-sdk-go Authors.
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

	decision "github.com/zchee/decision-model-sdk-go"
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
	patient := decision.DefaultRetry().
		MaxRetries(4).
		Backoff(time.Second, 10*time.Second, 0.25).
		Budget(time.Minute)
	client, err := decision.NewClient(decision.WithRetry(patient))
	if err != nil {
		return err
	}
	defer client.Close()

	questions, err := decision.NewQuestions().
		Noul("refund", decision.Noul{Instructions: decision.Text("Does the customer ask for a refund?")}).
		Prepare()
	if err != nil {
		return err
	}
	state := "I was charged twice; please refund one of the charges."

	// This call retries nothing; the client's policy applies to the others.
	response, err := client.SystemOne(ctx, state, questions, decision.Retry(decision.NoRetry()))
	report("without retries", response, err)

	// A successful response is billed, so one that fails validation is
	// never retried unless a predicate asks for it.
	validating := patient.Predicate(func(err error) bool {
		_, ok := errors.AsType[*decision.ResponseValidationError](err)
		return ok
	})
	response, err = client.SystemOne(ctx, state, questions, decision.Retry(validating))
	report("with a predicate", response, err)
	return nil
}

// report prints the answer, or what kind of error the call returned.
func report(label string, response *decision.SystemOneResponse, err error) {
	if err == nil {
		refund, _ := response.Answers().Noul("refund")
		fmt.Printf("%s: refund %.2f\n", label, refund.Noul())
		return
	}
	if apiErr, ok := errors.AsType[*decision.APIError](err); ok {
		wait, _ := apiErr.RetryAfter()
		fmt.Printf("%s: the API answered %d (%s, authentication %t, retry after %s)\n",
			label, apiErr.StatusCode, apiErr.Kind, apiErr.IsAuthentication(), wait)
		return
	}
	if timeoutErr, ok := errors.AsType[*decision.TimeoutError](err); ok {
		fmt.Printf("%s: no response within %s\n", label, timeoutErr.Timeout)
		return
	}
	if connErr, ok := errors.AsType[*decision.ConnectionError](err); ok {
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
discards its records without one.

<!-- example: logging/main.go -->
```go
// Copyright 2026 The decision-model-sdk-go Authors.
// SPDX-License-Identifier: Apache-2.0

// Command logging gives the client a log/slog logger. At INFO the client
// writes one record per attempt; at DEBUG it adds the request and response
// headers, with credentials shown as ***, and the body lengths; at
// decision.LevelTrace it adds the bodies themselves, as sent and received.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"

	decision "github.com/zchee/decision-model-sdk-go"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	client, err := decision.NewClient(decision.WithLogger(logger))
	if err != nil {
		return err
	}
	defer client.Close()

	questions, err := decision.NewQuestions().
		Noul("greeting", decision.Noul{Instructions: decision.Text("Is this a greeting?")}).
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

The port keeps the Python SDK's behaviour except where noted. The main
differences:

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
  `decision.LevelTrace` only.
- The client reads `DECISION_MODEL_API_KEY`, `DECISION_MODEL_BASE_URL` and
  `DECISION_MODEL_DEFAULT_MODEL`, never the Python SDK's `TYPESAFE_` names,
  and has no default base URL (`https://api.typesafe.ai` there) and no
  default model (`jev-latest` there): the SDK serves any vendor's decision
  model, so a default would silently send a key to one vendor or ask its
  model.

[`docs/deviations.md`](docs/deviations.md) is the full table, each row
naming the Python behaviour, the Go behaviour and why, and the upstream
tests it replaces; [`docs/port-test-matrix.md`](docs/port-test-matrix.md)
maps every one of the Python SDK's 129 tests to a Go test or to a row of
that table.

## Moving from the earlier module path

The module was renamed on 2026-10-03; [`CHANGELOG.md`](CHANGELOG.md)
names its earlier path, whose releases stay on the Go module proxy. A
program that used it changes four things:

- The import: `decision "github.com/zchee/decision-model-sdk-go"`, and the
  package is named `decision`.
- The struct tags of `Ask[T]`, `PreparedFor[T]` and `DecodeAs[T]`: their key
  is `decision`, as in `decision:"kind=noul"`, so rewrite each tag whose key
  was `typesafe` before. The old key is not read; an answer field that
  carries only it is refused as one with no tag is
  (`field has no decision tag`).
- The environment: `DECISION_MODEL_API_KEY`, `DECISION_MODEL_BASE_URL` and
  `DECISION_MODEL_DEFAULT_MODEL` replace the variables of the old names.
- The base URL and the model: there is no default for either, so a client
  names its vendor's base URL (`WithBaseURL` or the variable) and a model
  (`WithModel`, the variable, or `Model` on each call).

## Tests

`go test ./...` runs every test offline, the examples included (against
a local stand-in for the API). The tests against the live API are opt-in:
they compile only with the build tag `live`, and each fails before it
calls the API unless `DECISION_MODEL_LIVE_TESTS=1`, `DECISION_MODEL_API_KEY`,
`DECISION_MODEL_BASE_URL` and `DECISION_MODEL_DEFAULT_MODEL` are set: the
tests name no vendor's API or model in code. For TypeSafe AI's API:

```sh
DECISION_MODEL_LIVE_TESTS=1 DECISION_MODEL_API_KEY=... \
	DECISION_MODEL_BASE_URL=https://api.typesafe.ai \
	DECISION_MODEL_DEFAULT_MODEL=jev-latest \
	go test -tags live -count=1 -v ./livetest/
```

The key is read from the environment and is never printed. The API bills
each System One call; CI does not run these tests
([`docs/support.md`](docs/support.md#live-tests) says where they run).

## CI, coverage and benchmarks

- [CI](.github/workflows/ci.yaml) runs the linters (golangci-lint, which
  runs modernize, vet and staticcheck and checks every Go file's formatting;
  govulncheck; `go mod tidy -diff`), the port test matrix, the
  docs-snippets check, the check that every example is a package, and the
  compile-time refusal off the support matrix; then the tests, the
  import-confinement tests among them, with `-race` on `ubuntu-26.04`,
  `xcode-27` and `windows-2025`, and the allocation budgets without it.
- [Codecov](https://codecov.io/gh/zchee/decision-model-sdk-go) receives each
  image's coverage and measures the SDK and the adapter module apart:
  each one's project status blocks at the 85 % target (its 90 % goal and
  its patch status are informational), and every block the SDK's
  tests leave unrun has a reason in
  [`docs/uncovered-lines.md`](docs/uncovered-lines.md).
- [CodSpeed](https://codspeed.io/zchee/decision-model-sdk-go) runs every
  benchmark in walltime mode on `ubuntu-26.04`. The job fails when a whole
  call is not faster than a naive sonic client's in the same run (the step
  "AC-P7 gate" compares `BenchmarkCall/sdk`'s mean with
  `BenchmarkCall/naive`'s); every absolute time is reported, not enforced
  ([`docs/perf/codspeed.md`](docs/perf/codspeed.md)).

## The adapter module

[`adapter/`](adapter) holds a second Go module,
`github.com/zchee/decision-model-sdk-go/adapter`, a port of
[system-one-adapter-python](https://github.com/typesafe-ai/system-one-adapter-python)
0.2.1. It answers the System One API with an LLM, as the `RoundTripper` of
this SDK's `WithRoundTripper`, so that TypeSafe can be compared with an LLM
on cost, speed and intelligence. It is not released yet;
[`adapter/README.md`](adapter/README.md) describes it.

## License

Licensed under the Apache License, Version 2.0 ([LICENSE](LICENSE)).
Third-party licence texts are available from each module's source in the Go
module cache (`go mod download -json <module>` gives the directory).
