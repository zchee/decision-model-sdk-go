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
