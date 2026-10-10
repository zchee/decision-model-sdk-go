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

package decision

// NewClientWithEnv builds a client from opts as [NewClient] does, but reads
// the environment through getenv instead of the process's, so that a test
// in package decision_test can see every variable the client reads.
func NewClientWithEnv(getenv func(string) string, opts ...ClientOption) (*Client, error) {
	o := collectOptions(opts)
	cfg, err := o.resolve(getenv)
	if err != nil {
		return nil, err
	}
	return buildClient(&o, cfg)
}
