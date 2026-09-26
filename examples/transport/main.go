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
