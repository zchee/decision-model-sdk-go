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
