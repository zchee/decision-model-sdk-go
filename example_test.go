// Copyright 2026 The typesafe-sdk-go Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package typesafe_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	typesafe "github.com/zchee/typesafe-sdk-go"
)

// cannedAPI answers every request with one status and body. It stands in
// for the API, so that these examples run without the network.
type cannedAPI struct {
	status int
	body   string
}

// RoundTrip implements http.RoundTripper.
func (c cannedAPI) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		_, _ = io.Copy(io.Discard, req.Body)
		_ = req.Body.Close()
	}
	return &http.Response{
		StatusCode: c.status,
		Proto:      "HTTP/2.0",
		ProtoMajor: 2,
		Header: http.Header{
			"Content-Type":          {"application/json"},
			"X-Typesafe-Request-Id": {"req_example"},
		},
		Body:          io.NopCloser(strings.NewReader(c.body)),
		ContentLength: int64(len(c.body)),
		Request:       req,
	}, nil
}

// answersBody is an answer to the questions of the examples below.
const answersBody = `{"model":"jev-latest","usage":{"input_tokens":12,"output_tokens":3},"answers":{` +
	`"spam":{"type":"noul","noul":0.02},` +
	`"tone":{"type":"choice","choice":"friendly","confidence":0.9,"probabilities":{"friendly":0.9,"hostile":0.1}},` +
	`"quality":{"type":"score","score":1.7,"confidence":0.8,"legend":{"0":"bad","1":"ok","2":"great"},"probabilities":{"0":0.1,"1":0.1,"2":0.8}}}}`

// exampleClient returns a client whose requests the canned API answers.
func exampleClient(status int, body string) *typesafe.Client {
	c, err := typesafe.NewClient(
		typesafe.WithAPIKey("example-key"),
		typesafe.WithRoundTripper(cannedAPI{status: status, body: body}),
	)
	if err != nil {
		panic(err)
	}
	return c
}

// Review is a question set declared as a struct: each tagged field is a
// question, and receives its answer.
type Review struct {
	Spam    typesafe.NoulAnswer   `typesafe:"kind=noul;name=spam;instructions=Is this review spam?"`
	Tone    typesafe.ChoiceAnswer `typesafe:"kind=choice;name=tone;instructions=What is the tone?;options=friendly|hostile=rude or threatening"`
	Quality typesafe.ScoreAnswer  `typesafe:"kind=score;name=quality;instructions=How useful is the review?;levels=bad|ok|great"`
}

func ExampleNewQuestions() {
	questions, err := typesafe.NewQuestions().
		Noul("spam", typesafe.Noul{Instructions: typesafe.Text("Is this review spam?")}).
		Choice("tone", typesafe.Choice{
			Instructions: typesafe.Text("What is the tone?"),
			Options:      typesafe.Options{{Label: "friendly"}, {Label: "hostile", Description: typesafe.Text("rude or threatening")}},
		}).
		Score("quality", typesafe.Score{
			Instructions: typesafe.Text("How useful is the review?"),
			Levels:       []typesafe.Content{typesafe.Text("bad"), typesafe.Text("ok"), typesafe.Text("great")},
		}).
		Raw("language", typesafe.RawQuestion{Type: "choice", Fields: map[string]any{
			"instructions": "Which language is the review in?",
			"criteria":     map[string]any{"en": nil, "ja": nil},
		}}).
		Prepare()
	if err != nil {
		fmt.Println(err)
		return
	}
	for name := range questions.Names() {
		fmt.Println(name)
	}
	// Output:
	// spam
	// tone
	// quality
	// language
}

func ExampleQuestions_Prepare() {
	_, err := typesafe.NewQuestions().
		Noul("spam", typesafe.Noul{}).
		Noul("spam", typesafe.Noul{Instructions: typesafe.Text("Is it spam?")}).
		Prepare()
	_, ok := errors.AsType[*typesafe.ConfigError](err)
	fmt.Println(ok)
	fmt.Println(err)
	// Output:
	// true
	// Question "spam" is added more than once; question names must be unique.
}

func ExampleClient_SystemOne() {
	client := exampleClient(http.StatusOK, answersBody)
	defer client.Close()
	questions, err := typesafe.NewQuestions().
		Noul("spam", typesafe.Noul{Instructions: typesafe.Text("Is this review spam?")}).
		Choice("tone", typesafe.Choice{Options: typesafe.Options{{Label: "friendly"}, {Label: "hostile"}}}).
		Score("quality", typesafe.Score{Levels: []typesafe.Content{typesafe.Text("bad"), typesafe.Text("ok"), typesafe.Text("great")}}).
		Prepare()
	if err != nil {
		fmt.Println(err)
		return
	}

	state := map[string]any{"review": "Great product, fast shipping."}
	response, err := client.SystemOne(context.Background(), state, questions, typesafe.Timeout(5*time.Second))
	if err != nil {
		fmt.Println(err)
		return
	}
	answers := response.Answers()
	spam, _ := answers.Noul("spam")
	tone, _ := answers.Choice("tone")
	quality, _ := answers.Score("quality")
	level, _ := quality.Description(2)
	fmt.Printf("spam %.2f, tone %s, quality %.1f (level 2 is %q)\n", spam.Noul(), tone.Choice(), quality.Score(), level.Text())
	input, _ := response.Usage().InputTokens()
	id, _ := response.Meta().RequestID()
	fmt.Printf("model %s, %d input tokens, request %s\n", response.Model(), input, id)
	// Output:
	// spam 0.02, tone friendly, quality 1.7 (level 2 is "great")
	// model jev-latest, 12 input tokens, request req_example
}

func ExampleAsk() {
	client := exampleClient(http.StatusOK, answersBody)
	defer client.Close()

	review, err := typesafe.Ask[Review](context.Background(), client, "Great product, fast shipping.")
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Printf("spam %.2f\n", review.Spam.Noul())
	fmt.Printf("tone %s (%.1f)\n", review.Tone.Choice(), review.Tone.Confidence())
	fmt.Printf("quality %.1f\n", review.Quality.Score())
	// Output:
	// spam 0.02
	// tone friendly (0.9)
	// quality 1.7
}

func ExamplePreparedFor() {
	questions, err := typesafe.PreparedFor[Review]()
	if err != nil {
		fmt.Println(err)
		return
	}
	for name := range questions.Names() {
		fmt.Println(name)
	}
	// Output:
	// spam
	// tone
	// quality
}

func ExampleDecodeAs() {
	// A response stored earlier with MarshalJSON, read back.
	var stored typesafe.SystemOneResponse
	if err := stored.UnmarshalJSON([]byte(answersBody)); err != nil {
		fmt.Println(err)
		return
	}
	review, err := typesafe.DecodeAs[Review](&stored)
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(review.Tone.Choice(), review.Spam.Present())
	// Output:
	// friendly true
}

func ExampleAnswers_All() {
	var stored typesafe.SystemOneResponse
	if err := stored.UnmarshalJSON([]byte(answersBody)); err != nil {
		fmt.Println(err)
		return
	}
	for name, answer := range stored.Answers().All() {
		fmt.Println(name, answer.Kind())
	}
	// Output:
	// spam noul
	// tone choice
	// quality score
}

func ExampleSystemOneResponse_MarshalJSON() {
	var stored typesafe.SystemOneResponse
	if err := stored.UnmarshalJSON([]byte(`{"model":"jev-latest","usage":{"input_tokens":12},"answers":{"spam":{"type":"noul","noul":0.02}}}`)); err != nil {
		fmt.Println(err)
		return
	}
	payload, err := stored.MarshalJSON()
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(string(payload))
	// Output:
	// {"model":"jev-latest","usage":{"input_tokens":12,"output_tokens":null},"answers":{"spam":{"type":"noul","noul":0.02}}}
}

func ExampleModels_List() {
	client := exampleClient(http.StatusOK, `{"models":[{"name":"jev-latest","description":"The latest model","release_date":"2026-09-10"}]}`)
	defer client.Close()
	models, err := client.Models().List(context.Background())
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, m := range models.Models() {
		fmt.Println(m.Name(), m.ReleaseDate())
	}
	// Output:
	// jev-latest 2026-09-10
}

func ExampleAPIError() {
	client := exampleClient(http.StatusForbidden, `{"detail":{"error_type":"authentication_error","message":"Must supply an API key!"}}`)
	defer client.Close()
	_, err := client.Models().List(context.Background())

	// The error types are pointers: ask for *APIError (errors.As takes a
	// **APIError).
	if apiErr, ok := errors.AsType[*typesafe.APIError](err); ok {
		fmt.Println(apiErr.StatusCode, apiErr.Kind, apiErr.IsAuthentication())
		fmt.Println(apiErr.Message)
	}
	// Output:
	// 403 permission denied true
	// Must supply an API key!
}

func ExampleRetryPolicy() {
	// A policy is a value: each setter returns a changed copy. This one
	// retries twice, at once (a zero backoff), on top of the defaults.
	quick := typesafe.DefaultRetry().MaxRetries(2).Backoff(0, 0, 0)
	client, err := typesafe.NewClient(
		typesafe.WithAPIKey("example-key"),
		typesafe.WithRetry(quick),
		typesafe.WithRoundTripper(cannedAPI{status: http.StatusServiceUnavailable, body: `{"detail":"try again later"}`}),
	)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer client.Close()

	// The client's policy: the first attempt and two retries.
	_, err = client.Models().List(context.Background())
	fmt.Println(err != nil, client.Stats().Attempts)
	// One call's own policy: no retry.
	_, err = client.Models().List(context.Background(), typesafe.Retry(typesafe.NoRetry()))
	fmt.Println(err != nil, client.Stats().Attempts)
	// Output:
	// true 3
	// true 4
}

func ExampleText() {
	plain, _ := typesafe.Text("payments or invoices").MarshalJSON()
	structured, _ := typesafe.JSON([]byte(`{"summary": "payments", "examples": ["charged twice"]}`)).MarshalJSON()
	fmt.Println(string(plain))
	fmt.Println(string(structured))
	// Output:
	// "payments or invoices"
	// {"summary":"payments","examples":["charged twice"]}
}
