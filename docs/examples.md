# Examples

Each program under [`examples/`](../examples) is a package of this module
and a complete program; every Go block on this page, and in the
[README](../README.md), is one of them, byte for byte
(`.github/scripts/docs-snippets.py` checks it in CI). CI also compiles them
(`go vet ./examples/...`), and runs each against a local stand-in for the
API (`livetests.TestExamplesOffline`); `livetests.TestExamples` runs them
against the API itself (`-tags live`, not in CI).

Each program reads the API key from `TYPESAFE_API_KEY`, as every client
built without `WithAPIKey` does, and `TYPESAFE_BASE_URL` when it is set.
To run one, from the repository root, with the key in your environment:

```sh
go run ./examples/quickstart
```

| Program | Shows | Python SDK counterpart |
| --- | --- | --- |
| [`quickstart`](#quickstart) | one question, one answer | the README's quick start |
| [`typed`](#typed-answers) | a struct as the question set: `Ask[T]`, `PreparedFor[T]`, `DecodeAs[T]`, `optional` | `response_model=` (`tests/typing/pydantic_response_models.py`) |
| [`options`](#client-and-call-options) | state forms, a raw question, client options and per-call options | `tests/typing/valid.py` (its `extra_body` aside) |
| [`transport`](#transports) | `WithHTTPTransport` and `WithRoundTripper` | `transport=` / `http_client=` (`tests/typing/transport.py`) |
| [`retries`](#retries-and-errors) | retry policies and telling the errors apart | `RetryPolicy`, the exception classes |
| [`logging`](#logging) | a `log/slog` logger and what its records show | `logging` with `TYPESAFE_LOG_LEVEL` |
| [`concurrency`](#concurrency) | one client shared by goroutines over one HTTP/2 connection | `AsyncTypeSafeClient` |

The three positive fixtures of the Python SDK's `tests/typing/` check with
pyrefly that the public API's types line up; in Go the compiler does that
work, so their counterpart is that these programs compile (the port test
matrix, row XT1).

## Quickstart

One choice question about a support ticket. `NewClient` with no options
reads the key and the base URL from the environment and keeps the
defaults: model `jev-latest`, 10 s per attempt, two retries.

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

## Typed answers

A struct declares the questions in its field tags and receives the answers
in its fields. `Ask[T]` is `PreparedFor[T]`, `SystemOne` and `DecodeAs[T]`
in one call; the two-step form keeps the response, for its request id,
usage and raw body. A tag's `name` is the question's name on the wire (the
field name as written when it is left out); `optional` lets the answer be
missing, which `Present` reports. `PreparedFor[T]` checks the tags once per
type and returns a `*ConfigError` for a malformed one.

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

## Client and call options

The client's options set defaults for every call; a call's options
override them for that call alone: `Model`, `Timeout`, `Retry` and
`Header`. A state is text, a JSON object or an array: a string, a map, a
slice, a struct or `RawJSON`. A number, a boolean or `nil` is refused
before anything is sent. A `RawQuestion` is sent as it is. `ExtraBody`,
not used here, adds a member to the top level of the request body or
replaces `state`, `model` or `questions`; the API refuses a member it does
not know with a 400 `api_usage_error` (ledger W6.4-07).

<!-- example: options/main.go -->
```go
// Copyright 2026 The typesafe-sdk-go Authors.
// SPDX-License-Identifier: Apache-2.0

// Command options shows a client's settings and a call's: the forms a
// state may take, a question given as raw fields, and the options that
// override the client's deadline, retries and headers for one call.
package main

import (
	"context"
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

// Ticket is a state given as a struct; its JSON tags name its members.
type Ticket struct {
	Subject string   `json:"subject"`
	Tags    []string `json:"tags"`
}

func run(ctx context.Context) error {
	// The client's settings apply to every call it makes.
	client, err := typesafe.NewClient(
		typesafe.WithModel(typesafe.DefaultModel),
		typesafe.WithTimeout(30*time.Second),
		typesafe.WithRetry(typesafe.DefaultRetry().MaxRetries(1)),
		typesafe.WithHeader("X-Team", "support"),
		typesafe.WithUserAgentProduct("options-example/1.0"),
	)
	if err != nil {
		return err
	}
	defer client.Close()

	questions, err := typesafe.NewQuestions().
		Choice("topic", typesafe.Choice{
			Instructions: typesafe.Text("What is the message about?"),
			Options:      typesafe.Options{{Label: "billing", Description: typesafe.Text("payments or invoices")}, {Label: "other"}},
		}).
		Score("urgency", typesafe.Score{
			Levels: []typesafe.Content{typesafe.Text("low"), typesafe.Text("high")},
		}).
		// A question given as its raw fields is sent as it is.
		Raw("refund", typesafe.RawQuestion{Type: "noul", Fields: map[string]any{
			"instructions": "Does the customer ask for a refund?",
			"criteria": map[string]any{
				"true": map[string]any{"meaning": "a refund or a chargeback", "examples": []any{"please refund me"}},
			},
		}}).
		Prepare()
	if err != nil {
		return err
	}

	// A state is text, a JSON object or an array: a string, a map, a slice,
	// a struct or RawJSON sent as it is.
	states := []any{
		"I was charged twice.",
		map[string]any{"items": []any{"charged twice", nil}},
		Ticket{Subject: "Refund", Tags: []string{"billing"}},
		typesafe.RawJSON(`{"subject":"Refund","nullable":null}`),
	}
	for _, state := range states {
		response, err := client.SystemOne(ctx, state, questions,
			typesafe.Timeout(20*time.Second),
			typesafe.Retry(typesafe.NoRetry()),
			typesafe.Header("X-Call", "options-example"),
		)
		if err != nil {
			return err
		}
		topic, _ := response.Answers().Choice("topic")
		fmt.Printf("%T: %s (%d answers from %s)\n", state, topic.Choice(), response.Answers().Len(), response.Model())
	}

	// The models endpoint takes the call options that apply to it.
	models, err := client.Models().List(ctx,
		typesafe.Timeout(2*time.Second),
		typesafe.Retry(typesafe.DefaultRetry()),
		typesafe.Header("X-Call", "options-example"),
	)
	if err != nil {
		return err
	}
	for _, m := range models.Models() {
		fmt.Printf("model %s (%s)\n", m.Name(), m.ReleaseDate())
	}
	return nil
}
```

## Transports

`WithHTTPTransport` takes an `*http.Transport` and uses a clone of it: its
dialer, TLS and proxy settings stay, and the SDK adds its HTTP/2 policy (one
connection, ALPN checked on the API's handshake, the cold-start gate).
`WithRoundTripper` takes any `http.RoundTripper` and sends every request
through it as it is; the deadline and the response size limit still apply.
A RoundTripper must not modify the request: the SDK hands every call the
same header map.

<!-- example: transport/main.go -->
```go
// Copyright 2026 The typesafe-sdk-go Authors.
// SPDX-License-Identifier: Apache-2.0

// Command transport gives the client a transport of its own: an
// *http.Transport the SDK clones and configures, or any http.RoundTripper,
// used as it is.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	typesafe "github.com/zchee/typesafe-sdk-go"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

// countingTransport counts the requests it sends. Like every RoundTripper
// it must not modify the request.
type countingTransport struct {
	base http.RoundTripper
	n    atomic.Int64
}

func (t *countingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.n.Add(1)
	return t.base.RoundTrip(req)
}

func run(ctx context.Context) error {
	// WithHTTPTransport: the SDK uses a clone of the transport, which keeps
	// its dialer, TLS and proxy settings, and adds its HTTP/2 policy.
	tuned := &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		IdleConnTimeout: 5 * time.Minute,
	}
	client, err := typesafe.NewClient(typesafe.WithHTTPTransport(tuned))
	if err != nil {
		return err
	}
	defer client.Close()
	var models *typesafe.ModelsResponse
	if models, err = client.Models().List(ctx); err != nil {
		return err
	}
	fmt.Printf("%d model(s)\n", len(models.Models()))

	// WithRoundTripper: requests go through the RoundTripper as they are.
	// The client's deadline and response size limit still apply; the
	// SDK's connection policy does not.
	counter := &countingTransport{base: http.DefaultTransport}
	opaque, err := typesafe.NewClient(typesafe.WithRoundTripper(counter))
	if err != nil {
		return err
	}
	defer opaque.Close()
	questions, err := typesafe.NewQuestions().
		Noul("question", typesafe.Noul{Instructions: typesafe.Text("Is this a question?")}).
		Prepare()
	if err != nil {
		return err
	}
	var response *typesafe.SystemOneResponse
	if response, err = opaque.SystemOne(ctx, "Is the transport configurable?", questions); err != nil {
		return err
	}
	answer, _ := response.Answers().Noul("question")
	fmt.Printf("question: %.2f after %d request(s)\n", answer.Noul(), counter.n.Load())
	return nil
}
```

## Retries and errors

A `RetryPolicy` is a value whose setters return a changed copy; its zero
value is `DefaultRetry`, the Python SDK's `RetryPolicy()`. `WithRetry` sets
a client's policy and `Retry` one call's. A successful response is billed,
so a 2xx is never retried by its status, and a response that fails
validation is retried only when a `Predicate` accepts it. Each error type
is a pointer: ask `errors.AsType` for `*APIError`, not `APIError`. A
cancelled context returns `context.Canceled` itself.

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

## Logging

The client writes to the `*slog.Logger` given by `WithLogger` and discards
its records without one; it never reads `TYPESAFE_LOG_LEVEL` and never
sets a level. INFO holds one record per attempt (method, endpoint, status,
duration, request id); DEBUG adds the request and response headers and the
body lengths; `typesafe.LevelTrace` adds the bodies. Header values that
are credentials, or hold the client's key, are shown as `***` in the
records and in the errors; text that comes from a response body, a
server's message or an answer's name, is shown as the server sent it, and
a `LevelTrace` body record is the body itself.

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

## Concurrency

A `*Client` is safe for concurrent use and so is a prepared question set.
Over HTTPS the client holds one HTTP/2 connection to the API and sends each
call as a stream on it; `WarmUp` opens it before a burst. `Stats` counts the
connections dialled and the attempts made. The API accepts 100 concurrent
streams on a connection (ledger W6.4-04); the client queues the calls
beyond that on the same connection.

<!-- example: concurrency/main.go -->
```go
// Copyright 2026 The typesafe-sdk-go Authors.
// SPDX-License-Identifier: Apache-2.0

// Command concurrency shares one client between goroutines. Over HTTPS the
// client keeps one HTTP/2 connection to the API and sends each call on it
// as a stream of its own; WarmUp opens that connection before the calls.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"

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
	if err := client.WarmUp(ctx); err != nil {
		return err
	}

	// A prepared question set is read-only: every goroutine can ask it.
	questions, err := typesafe.NewQuestions().
		Choice("sentiment", typesafe.Choice{
			Instructions: typesafe.Text("What is the sentiment of the review?"),
			Options:      typesafe.Options{{Label: "positive"}, {Label: "neutral"}, {Label: "negative"}},
		}).
		Prepare()
	if err != nil {
		return err
	}

	reviews := []string{
		"Arrived early and works perfectly.",
		"It does what it says.",
		"Broke after two days.",
	}
	sentiments := make([]string, len(reviews))
	errs := make([]error, len(reviews))
	var wg sync.WaitGroup
	for i, review := range reviews {
		wg.Go(func() {
			response, err := client.SystemOne(ctx, review, questions)
			if err != nil {
				errs[i] = err
				return
			}
			answer, _ := response.Answers().Choice("sentiment")
			sentiments[i] = answer.Choice()
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}
	for i, review := range reviews {
		fmt.Printf("%-36q %s\n", review, sentiments[i])
	}
	stats := client.Stats()
	fmt.Printf("%d attempts on %d connection(s)\n", stats.Attempts, stats.Dials)
	return nil
}
```
