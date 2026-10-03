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

// Package testsupport holds the test doubles and measurement helpers the
// SDK's tests share: servers, listeners, dialers and a proxy for transport
// tests ([LoopbackServer], [SilentListener], [RefusedAddr], [GatedDialer],
// [FakeH2CServer], [Proxy]); request and log doubles ([Recorder],
// [FakeAPI], [LogRecorder]); allocation measurement ([Measure],
// [MeasureMin], [Spread], [FloorCall]); response bodies, from the module's
// testdata directory and generated ([Fixture], [UnknownAnswerFlood]); and
// the fuzz targets' per-input bound and guard pages ([BoundFuzzInput],
// [NewGuard]).
//
// The package imports neither the SDK's root package nor internal/codec, so
// the packages that test the transport can use it without sonic. It is the
// only package of the module that imports golang.org/x/net.
package testsupport
