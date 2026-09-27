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

package typesafe

import "github.com/zchee/typesafe-sdk-go/internal/engine"

// Header redaction and the API-key needle are internal/engine's (rulings
// R68, R87, R93); these names keep the root package's call sites as they
// were.

// redacted is what a credential is printed as ([engine.Redacted]).
const redacted = engine.Redacted

// headerRedactor is [engine.HeaderRedactor].
type headerRedactor = engine.HeaderRedactor

// keyNeedle is [engine.KeyNeedle].
func keyNeedle(key string) bool { return engine.KeyNeedle(key) }

// redactor returns the redactor for the client c configures: its API key
// and no proxy's credentials, which [engine.Transport.ResponseRedactor] adds for
// the response that may hold them.
func (c *config) redactor() headerRedactor { return engine.NewHeaderRedactor(c.apiKey) }
