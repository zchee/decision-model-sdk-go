// Copyright 2026 The decision-model-sdk-go Authors.
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

package cassette

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	gocmp "github.com/google/go-cmp/cmp"

	"github.com/zchee/decision-model-sdk-go/adapter/internal/jsonx"
)

var recordedProviderIDPattern = regexp.MustCompile(`^(resp_|chatcmpl-|msg_|req_|fc_|rs_|v1_)[A-Za-z0-9_-]+$`)

// checkRecordedBody checks every string for a known identifier pattern and
// refuses any non-placeholder string at an assigned-identifier path, including
// unknown formats the scrubber cannot recognize. It never includes values in errors.
func checkRecordedBody(body jsonx.Node, response bool) error {
	if body.Kind() == jsonx.KindObject {
		for i := range body.Len() {
			name, child := body.Name(i), body.Index(i)
			rootID := response && name == "id" || !response && (name == "previous_response_id" || name == "previous_interaction_id")
			if rootID && child.Kind() == jsonx.KindString && child.Text() != "x" {
				return fmt.Errorf("unreplaced identifier at $.%s", name)
			}
			items := response && (name == "output" || name == "steps") || !response && name == "input"
			if items && child.Kind() == jsonx.KindArray {
				for j := range child.Len() {
					id, exists := child.Index(j).Member("id")
					if exists && id.Kind() == jsonx.KindString && id.Text() != "x" {
						return fmt.Errorf("unreplaced identifier at $.%s[%d].id", name, j)
					}
				}
			}
		}
	}
	return checkRecordedStrings(body)
}

func checkRecordedStrings(node jsonx.Node) error {
	if node.Kind() == jsonx.KindString && recordedProviderIDPattern.MatchString(node.Text()) {
		return errors.New("retained provider identifier pattern; value withheld")
	}
	for i := range node.Len() {
		if err := checkRecordedStrings(node.Index(i)); err != nil {
			return err
		}
	}
	return nil
}

// checkGoRecordings walks only the port's new recordings, never upstream fixtures.
// Missing directories pass; unreadable or malformed recordings fail closed.
func checkGoRecordings(dir string) error {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking recording directory: %w", err)
	}
	defer root.Close()
	return fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symbolic-link recording is not inspectable")
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := root.ReadFile(path)
		if err != nil {
			return errors.New("recording is unreadable")
		}
		document, err := jsonx.Read(data)
		if err != nil {
			return errors.New("recording is not valid JSON")
		}
		interactions, exists := document.Member("interactions")
		if !exists || interactions.Kind() != jsonx.KindArray {
			return errors.New("recording has no interaction array")
		}
		for i := range interactions.Len() {
			interaction := interactions.Index(i)
			request, exists := interaction.Member("request")
			if !exists || request.Kind() != jsonx.KindObject {
				return errors.New("recording has no request object")
			}
			body, exists := request.Member("body")
			if !exists {
				return errors.New("recording has no request body")
			}
			if err := checkRecordedBody(body, false); err != nil {
				return fmt.Errorf("request body at interaction %d: %w", i, err)
			}
			response, exists := interaction.Member("response")
			if !exists || response.Kind() != jsonx.KindObject {
				return errors.New("recording has no response object")
			}
			wrapper, exists := response.Member("body")
			if !exists || wrapper.Kind() != jsonx.KindObject {
				return errors.New("recording has no response body wrapper")
			}
			body, exists = wrapper.Member("string")
			if !exists {
				return errors.New("recording has no response body string")
			}
			if err := checkRecordedBody(body, true); err != nil {
				return fmt.Errorf("response body at interaction %d: %w", i, err)
			}
		}
		return nil
	})
}

func TestGoRecordingProviderIDs(t *testing.T) {
	if err := checkGoRecordings(filepath.Join("..", "..", "testdata", "cassettes-go")); err != nil {
		t.Fatal(err)
	}
}

func TestRecordedBodyInspection(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		body     string
		response bool
		wantErr  bool
	}{
		"success: response placeholders":                {body: `{"id":"x","output":[{"id":"x"}],"steps":[{"id":"x","name":"evaluation","signature":"synthetic-signed-state"}]}`, response: true},
		"success: request placeholders":                 {body: `{"previous_response_id":"x","previous_interaction_id":"x","input":[{"id":"x"}]}`},
		"success: ordinary names":                       {body: `{"questions":[{"id":"rating"}],"tools":[{"name":"evaluation"}],"metadata":{"id":null}}`},
		"success: nonstring ids":                        {body: `{"id":42,"output":[{"id":null},{"id":false},null,[]]}`, response: true},
		"success: request root is not assigned":         {body: `{"id":"rating"}`},
		"success: incomplete prefix":                    {body: `{"note":"resp_"}`},
		"success: whitespace is not identifier pattern": {body: `{"note":"resp_ordinary text"}`},
		"error: response opaque id":                     {body: `{"id":"opaqueMadeUp"}`, response: true, wantErr: true},
		"error: response novel id shape":                {body: `{"id":"future.id/shape"}`, response: true, wantErr: true},
		"error: response empty id":                      {body: `{"id":""}`, response: true, wantErr: true},
		"error: response output opaque id":              {body: `{"output":[{"id":"opaqueMadeUp"}]}`, response: true, wantErr: true},
		"error: response step opaque id":                {body: `{"steps":[{"id":"opaqueMadeUp"}]}`, response: true, wantErr: true},
		"error: request prior response opaque id":       {body: `{"previous_response_id":"opaqueMadeUp"}`, wantErr: true},
		"error: request prior interaction opaque id":    {body: `{"previous_interaction_id":"opaqueMadeUp"}`, wantErr: true},
		"error: request input opaque id":                {body: `{"input":[{"id":"opaqueMadeUp"}]}`, wantErr: true},
		"error: unexpected nested identifier":           {body: `{"metadata":{"id":"msg_CedarMadeUp"}}`, wantErr: true},
		"error: unexpected array identifier":            {body: `["req_CedarMadeUp"]`, wantErr: true},
		"error: response scalar identifier":             {body: `"fc_CedarMadeUp"`, response: true, wantErr: true},
	}
	for _, prefix := range []string{"resp_", "chatcmpl-", "msg_", "req_", "fc_", "rs_", "v1_"} {
		tests["error: anywhere prefix "+prefix] = struct {
			body     string
			response bool
			wantErr  bool
		}{body: `{"unlisted":["` + prefix + `CedarMadeUp"]}`, wantErr: true}
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			node, err := jsonx.Read([]byte(tt.body))
			if err != nil {
				t.Fatal("invalid synthetic test body")
			}
			err = checkRecordedBody(node, tt.response)
			if diff := gocmp.Diff(tt.wantErr, err != nil); diff != "" {
				t.Errorf("recording body inspection (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGoRecordingInspection(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		request  string
		response string
		missing  bool
		invalid  bool
		wantErr  bool
	}{
		"success: directory absent":        {missing: true},
		"success: clean recording":         {request: `{}`, response: `{"id":"x"}`},
		"success: scalar bodies":           {request: `""`, response: `"ordinary"`},
		"error: provider id planted":       {request: `{}`, response: `{"metadata":{"id":"msg_CedarMadeUp"}}`, wantErr: true},
		"error: opaque id planted":         {request: `{}`, response: `{"id":"future-format"}`, wantErr: true},
		"error: request reference planted": {request: `{"previous_interaction_id":"future-format"}`, response: `{}`, wantErr: true},
		"error: malformed recording":       {invalid: true, wantErr: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "cassettes-go")
			if !tt.missing {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				document := `{"interactions":[{"request":{"body":` + tt.request + `},"response":{"body":{"string":` + tt.response + `}}}]}`
				if tt.invalid {
					document = "not JSON"
				}
				if err := os.WriteFile(filepath.Join(dir, "made-up.json"), []byte(document), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			err := checkGoRecordings(dir)
			if diff := gocmp.Diff(tt.wantErr, err != nil); diff != "" {
				t.Errorf("Go recording inspection (-want +got):\n%s", diff)
			}
		})
	}
}
