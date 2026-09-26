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
