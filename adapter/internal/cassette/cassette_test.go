// Copyright 2026 The decision-model-sdk-go Authors.
// Portions ported from system-one-adapter-python (MIT, see LICENSE-UPSTREAM).
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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// writeCassette writes doc to a file of its own and returns the path.
func writeCassette(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cassette.json")
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// minimalCassette is a one-interaction document in the committed files'
// format, with a float spelled 1.0 and non-ASCII text in both bodies so
// the loader's re-encoding is observable.
const minimalCassette = `{
    "version": 1,
    "interactions": [
        {
            "request": {
                "method": "POST",
                "uri": "https://example.test/v1/x",
                "body": {
                    "q": 1.0,
                    "s": "héllo"
                },
                "headers": {
                    "content-type": [
                        "application/json"
                    ]
                }
            },
            "response": {
                "status": {
                    "code": 200,
                    "message": "OK"
                },
                "headers": {
                    "content-type": [
                        "application/json"
                    ]
                },
                "body": {
                    "string": {
                        "ok": true,
                        "f": 1.0,
                        "t": "日本語"
                    }
                }
            }
        }
    ]
}
`

func TestLoadMinimalCassette(t *testing.T) {
	t.Parallel()
	c, err := Load(writeCassette(t, minimalCassette))
	if err != nil {
		t.Fatal(err)
	}
	want := &Cassette{Interactions: []Interaction{{
		Request: Request{
			Method:     "POST",
			URI:        "https://example.test/v1/x",
			Body:       []byte(`{"q":1.0,"s":"héllo"}`),
			BodyIsJSON: true,
			Headers:    map[string][]string{"content-type": {"application/json"}},
		},
		Response: Response{
			StatusCode:    200,
			StatusMessage: "OK",
			Headers:       map[string][]string{"content-type": {"application/json"}},
			Body:          []byte(`{"ok":true,"f":1.0,"t":"日本語"}`),
			BodyIsJSON:    true,
		},
	}}}
	if diff := cmp.Diff(want, c); diff != "" {
		t.Errorf("Load (-want +got):\n%s", diff)
	}
}

func TestLoadStringBody(t *testing.T) {
	t.Parallel()
	doc := strings.Replace(minimalCassette, `{
                    "q": 1.0,
                    "s": "héllo"
                }`, `"plain text"`, 1)
	c, err := Load(writeCassette(t, doc))
	if err != nil {
		t.Fatal(err)
	}
	req := c.Interactions[0].Request
	if req.BodyIsJSON {
		t.Errorf("BodyIsJSON = true for a stored string body")
	}
	if got, want := string(req.Body), "plain text"; got != want {
		t.Errorf("Body = %q, want %q", got, want)
	}
}

func TestLoadCommittedCassette(t *testing.T) {
	t.Parallel()
	c, err := Load(filepath.Join("..", "..", "testdata", "cassettes", "test_live_typesafe_response_matches_reference_shape.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(c.Interactions), 1; got != want {
		t.Fatalf("len(Interactions) = %d, want %d", got, want)
	}
	ix := c.Interactions[0]
	if got, want := ix.Request.Method, "POST"; got != want {
		t.Errorf("Method = %q, want %q", got, want)
	}
	if got, want := ix.Request.URI, "https://api.typesafe.ai/v1/systemone"; got != want {
		t.Errorf("URI = %q, want %q", got, want)
	}
	if !ix.Request.BodyIsJSON || !ix.Response.BodyIsJSON {
		t.Errorf("BodyIsJSON = %t, %t for the recorded JSON bodies, want true, true", ix.Request.BodyIsJSON, ix.Response.BodyIsJSON)
	}
	if got, want := ix.Response.StatusCode, 200; got != want {
		t.Errorf("StatusCode = %d, want %d", got, want)
	}
	wantHeaders := map[string][]string{"content-type": {"application/json"}}
	if diff := cmp.Diff(wantHeaders, ix.Response.Headers); diff != "" {
		t.Errorf("response headers (-want +got):\n%s", diff)
	}
}

func TestLoadRefusals(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		doc  string
		want string
	}{
		"error: document not an object": {
			doc:  `[]`,
			want: "the document is not an object",
		},
		"error: unknown top-level member": {
			doc:  `{"version": 1, "interactions": [], "extra": 0}`,
			want: `the document has the unknown member "extra"`,
		},
		"error: missing interactions": {
			doc:  `{"version": 1}`,
			want: `the document is missing the member "interactions"`,
		},
		"error: version not one": {
			doc:  `{"version": 2, "interactions": []}`,
			want: "version is not the number 1",
		},
		"error: version a string": {
			doc:  `{"version": "1", "interactions": []}`,
			want: "version is not the number 1",
		},
		"error: interactions not an array": {
			doc:  `{"version": 1, "interactions": {}}`,
			want: "interactions is not an array",
		},
		"error: interaction not an object": {
			doc:  `{"version": 1, "interactions": [7]}`,
			want: "interactions[0] is not an object",
		},
		"error: interaction unknown member": {
			doc:  `{"version": 1, "interactions": [{"request": {}, "response": {}, "recorded_at": ""}]}`,
			want: `interactions[0] has the unknown member "recorded_at"`,
		},
		"error: request missing method": {
			doc:  `{"version": 1, "interactions": [{"request": {"uri": "u", "body": "b", "headers": {}}, "response": {}}]}`,
			want: `interactions[0].request is missing the member "method"`,
		},
		"error: request method not a string": {
			doc:  `{"version": 1, "interactions": [{"request": {"method": 1, "uri": "u", "body": "b", "headers": {}}, "response": {}}]}`,
			want: "interactions[0].request.method is not a string",
		},
		"error: request uri not a string": {
			doc:  `{"version": 1, "interactions": [{"request": {"method": "GET", "uri": 1, "body": "b", "headers": {}}, "response": {}}]}`,
			want: "interactions[0].request.uri is not a string",
		},
		"error: request body null": {
			doc:  `{"version": 1, "interactions": [{"request": {"method": "GET", "uri": "u", "body": null, "headers": {}}, "response": {}}]}`,
			want: "interactions[0].request.body is neither a JSON object or array nor a string",
		},
		"error: request headers not an object": {
			doc:  `{"version": 1, "interactions": [{"request": {"method": "GET", "uri": "u", "body": "b", "headers": []}, "response": {}}]}`,
			want: "interactions[0].request.headers is not an object",
		},
		"error: header value not an array": {
			doc:  `{"version": 1, "interactions": [{"request": {"method": "GET", "uri": "u", "body": "b", "headers": {"host": "h"}}, "response": {}}]}`,
			want: "interactions[0].request.headers.host is not an array",
		},
		"error: header element not a string": {
			doc:  `{"version": 1, "interactions": [{"request": {"method": "GET", "uri": "u", "body": "b", "headers": {"host": [1]}}, "response": {}}]}`,
			want: "interactions[0].request.headers.host[0] is not a string",
		},
		"error: response missing status": {
			doc:  `{"version": 1, "interactions": [{"request": {"method": "GET", "uri": "u", "body": "b", "headers": {}}, "response": {"headers": {}, "body": {"string": "s"}}}]}`,
			want: `interactions[0].response is missing the member "status"`,
		},
		"error: status code not an integer": {
			doc:  `{"version": 1, "interactions": [{"request": {"method": "GET", "uri": "u", "body": "b", "headers": {}}, "response": {"status": {"code": 200.5, "message": "OK"}, "headers": {}, "body": {"string": "s"}}}]}`,
			want: "interactions[0].response.status.code is not an integer",
		},
		"error: status message not a string": {
			doc:  `{"version": 1, "interactions": [{"request": {"method": "GET", "uri": "u", "body": "b", "headers": {}}, "response": {"status": {"code": 200, "message": 0}, "headers": {}, "body": {"string": "s"}}}]}`,
			want: "interactions[0].response.status.message is not a string",
		},
		"error: response body without string": {
			doc:  `{"version": 1, "interactions": [{"request": {"method": "GET", "uri": "u", "body": "b", "headers": {}}, "response": {"status": {"code": 200, "message": "OK"}, "headers": {}, "body": {}}}]}`,
			want: `interactions[0].response.body is missing the member "string"`,
		},
		"error: response body string a number": {
			doc:  `{"version": 1, "interactions": [{"request": {"method": "GET", "uri": "u", "body": "b", "headers": {}}, "response": {"status": {"code": 200, "message": "OK"}, "headers": {}, "body": {"string": 3}}}]}`,
			want: "interactions[0].response.body.string is neither a JSON object or array nor a string",
		},
		"error: not a JSON text": {
			doc:  `{`,
			want: "jsonx",
		},
		"error: status with an unknown member": {
			doc:  `{"version": 1, "interactions": [{"request": {"method": "GET", "uri": "u", "body": "b", "headers": {}}, "response": {"status": {"code": 200, "message": "OK", "extra": 1}, "headers": {}, "body": {"string": "s"}}}]}`,
			want: `interactions[0].response.status has the unknown member "extra"`,
		},
		"error: response headers not an object": {
			doc:  `{"version": 1, "interactions": [{"request": {"method": "GET", "uri": "u", "body": "b", "headers": {}}, "response": {"status": {"code": 200, "message": "OK"}, "headers": [], "body": {"string": "s"}}}]}`,
			want: "interactions[0].response.headers is not an object",
		},
		"error: status code an integer out of int range": {
			doc:  `{"version": 1, "interactions": [{"request": {"method": "GET", "uri": "u", "body": "b", "headers": {}}, "response": {"status": {"code": 123456789012345678901234567890, "message": "OK"}, "headers": {}, "body": {"string": "s"}}}]}`,
			want: "interactions[0].response.status.code",
		},
		"error: a body nested past the write depth": {
			doc: `{"version": 1, "interactions": [{"request": {"method": "GET", "uri": "u", "body": "b", "headers": {}}, "response": {"status": {"code": 200, "message": "OK"}, "headers": {}, "body": {"string": ` +
				strings.Repeat("[", 300) + "1" + strings.Repeat("]", 300) + `}}}]}`,
			want: "interactions[0].response.body.string",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := Load(writeCassette(t, tt.doc))
			if err == nil {
				t.Fatalf("Load succeeded, want an error containing %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()
	if _, err := Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("Load of a missing file succeeded, want an error")
	}
}
