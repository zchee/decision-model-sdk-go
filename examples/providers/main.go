// Copyright 2026 The decision-model-sdk-go Authors.
// SPDX-License-Identifier: Apache-2.0

// Command providers asks the same question of two vendors through the
// provider package's presets: Codiv, which speaks System One, with the model
// named on the client, and OpenAI, whose preset carries a dialect for its
// own wire and a default model. Each client reads its key from its vendor's
// own variable, CODIV_API_KEY and OPENAI_API_KEY, and from no other.
package main

import (
	"context"
	"fmt"
	"log"

	decision "github.com/zchee/decision-model-sdk-go"
	"github.com/zchee/decision-model-sdk-go/provider"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context) error {
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

	// A System One vendor: no preset names a model for it, so the client does.
	codiv, err := decision.NewClient(decision.WithProvider(provider.Codiv()), decision.WithModel("openjev-latest"))
	if err != nil {
		return err
	}
	defer codiv.Close()
	if err := ask(ctx, "Codiv", codiv, state, questions); err != nil {
		return err
	}

	// A vendor with a wire of its own: the preset's dialect rewrites the call
	// and reads the answer back, and its default model, gpt-6-luna, is asked.
	openai, err := decision.NewClient(decision.WithProvider(provider.OpenAI()))
	if err != nil {
		return err
	}
	defer openai.Close()
	return ask(ctx, "OpenAI", openai, state, questions)
}

// ask asks client the questions about state and prints the answer under the
// vendor's name.
func ask(ctx context.Context, vendor string, client *decision.Client, state any, questions *decision.Prepared) error {
	response, err := client.SystemOne(ctx, state, questions)
	if err != nil {
		return err
	}
	category, _ := response.Answers().Choice("category")
	fmt.Printf("%s (%s): %s\n", vendor, response.Model(), category.Choice())
	return nil
}
