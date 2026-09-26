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
