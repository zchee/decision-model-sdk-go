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
