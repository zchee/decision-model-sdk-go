// Copyright 2026 The decision-model-sdk-go Authors.
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

	decision "github.com/zchee/decision-model-sdk-go"
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
	// The client's settings apply to every call it makes; the API key, the
	// base URL and the model come from the environment.
	client, err := decision.NewClient(
		decision.WithTimeout(30*time.Second),
		decision.WithRetry(decision.DefaultRetry().MaxRetries(1)),
		decision.WithHeader("X-Team", "support"),
		decision.WithUserAgentProduct("options-example/1.0"),
	)
	if err != nil {
		return err
	}
	defer client.Close()

	questions, err := decision.NewQuestions().
		Choice("topic", decision.Choice{
			Instructions: decision.Text("What is the message about?"),
			Options:      decision.Options{{Label: "billing", Description: decision.Text("payments or invoices")}, {Label: "other"}},
		}).
		Score("urgency", decision.Score{
			Levels: []decision.Content{decision.Text("low"), decision.Text("high")},
		}).
		// A question given as its raw fields is sent as it is.
		Raw("refund", decision.RawQuestion{Type: "noul", Fields: map[string]any{
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
		decision.RawJSON(`{"subject":"Refund","nullable":null}`),
	}
	for _, state := range states {
		response, err := client.SystemOne(ctx, state, questions,
			decision.Timeout(20*time.Second),
			decision.Retry(decision.NoRetry()),
			decision.Header("X-Call", "options-example"),
		)
		if err != nil {
			return err
		}
		topic, _ := response.Answers().Choice("topic")
		fmt.Printf("%T: %s (%d answers from %s)\n", state, topic.Choice(), response.Answers().Len(), response.Model())
	}

	// The models endpoint takes the call options that apply to it.
	models, err := client.Models().List(ctx,
		decision.Timeout(2*time.Second),
		decision.Retry(decision.DefaultRetry()),
		decision.Header("X-Call", "options-example"),
	)
	if err != nil {
		return err
	}
	for _, m := range models.Models() {
		fmt.Printf("model %s (%s)\n", m.Name(), m.ReleaseDate())
	}
	return nil
}
