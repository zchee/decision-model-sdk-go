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
