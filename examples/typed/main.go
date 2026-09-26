// Copyright 2026 The typesafe-sdk-go Authors.
// SPDX-License-Identifier: Apache-2.0

// Command typed declares its questions as a struct: each tagged field is a
// question, and receives its answer.
package main

import (
	"context"
	"fmt"
	"log"

	typesafe "github.com/zchee/typesafe-sdk-go"
)

// Ticket is the question set. The tag's name key is the question's name on
// the wire (the field name when left out); optional marks an answer that
// may be missing from the response, which Present then reports.
type Ticket struct {
	Billing typesafe.NoulAnswer   `typesafe:"kind=noul;name=billing;instructions=Is this ticket about billing?"`
	Tone    typesafe.ChoiceAnswer `typesafe:"kind=choice;name=tone;instructions=What is the customer's tone?;options=calm|frustrated|angry"`
	Urgency typesafe.ScoreAnswer  `typesafe:"kind=score;name=urgency;instructions=How urgent is this ticket?;levels=can wait|this week|today"`
	Spam    typesafe.NoulAnswer   `typesafe:"kind=noul;name=spam;optional;instructions=Is this spam?"`
}

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

	state := map[string]any{
		"subject": "Charged twice this month",
		"body":    "I see two charges of $49. Please fix this ASAP.",
	}

	// One step: Ask sends the questions Ticket declares and returns the
	// answers as a Ticket.
	ticket, err := typesafe.Ask[Ticket](ctx, client, state)
	if err != nil {
		return err
	}
	fmt.Printf("billing: %.2f\n", ticket.Billing.Noul())
	fmt.Printf("tone: %s (confidence %.2f)\n", ticket.Tone.Choice(), ticket.Tone.Confidence())
	fmt.Printf("urgency: %.2f\n", ticket.Urgency.Score())
	if ticket.Spam.Present() {
		fmt.Printf("spam: %.2f\n", ticket.Spam.Noul())
	}

	// Two steps, when the response itself is needed too (its request id,
	// usage or raw body): the questions Ask would send, the call, and the
	// typed decode of its answers.
	questions, err := typesafe.PreparedFor[Ticket]()
	if err != nil {
		return err
	}
	response, err := client.SystemOne(ctx, state, questions)
	if err != nil {
		return err
	}
	again, err := typesafe.DecodeAs[Ticket](response)
	if err != nil {
		return err
	}
	id, _ := response.Meta().RequestID()
	fmt.Printf("request %s: tone %s\n", id, again.Tone.Choice())
	return nil
}
