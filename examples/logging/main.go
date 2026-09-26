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
