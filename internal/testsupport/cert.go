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

package testsupport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"
)

// certBundle is the self-signed certificate every TLS listener of this
// package serves.
type certBundle struct {
	pair tls.Certificate
	leaf *x509.Certificate
}

// testCert generates the certificate once per process.
var testCert = sync.OnceValues(newTestCert)

// newTestCert returns a self-signed ECDSA P-256 certificate for 127.0.0.1,
// ::1, localhost, example.com and *.example.com. It is valid from 1970 to
// 2100, so a clock faked by testing/synctest (which starts in 2000) still
// accepts it.
func newTestCert() (certBundle, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return certBundle{}, fmt.Errorf("testsupport: generate key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"typesafe-sdk-go testsupport"}},
		NotBefore:             time.Unix(0, 0),
		NotAfter:              time.Date(2100, time.January, 1, 0, 0, 0, 0, time.UTC),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost", "example.com", "*.example.com"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return certBundle{}, fmt.Errorf("testsupport: create certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return certBundle{}, fmt.Errorf("testsupport: parse certificate: %w", err)
	}
	return certBundle{
		pair: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf},
		leaf: leaf,
	}, nil
}

// RootCAs returns a fresh pool that trusts the certificate every TLS listener
// of this package serves (the loopback server and the TLS proxies). The
// certificate covers 127.0.0.1, ::1, localhost, example.com and
// *.example.com.
func RootCAs(tb testing.TB) *x509.CertPool {
	tb.Helper()
	cert := mustCert(tb)
	pool := x509.NewCertPool()
	pool.AddCert(cert.leaf)
	return pool
}

// ClientTLSConfig returns a client configuration that trusts the package's
// certificate and requires TLS 1.2 or later. The caller sets NextProtos (or
// lets net/http set them from its Protocols).
func ClientTLSConfig(tb testing.TB) *tls.Config {
	tb.Helper()
	return &tls.Config{RootCAs: RootCAs(tb), MinVersion: tls.VersionTLS12}
}

// serverTLSConfig returns the server side of the package's certificate with
// the given ALPN protocols (nil: no ALPN).
func serverTLSConfig(cert certBundle, nextProtos []string) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{cert.pair},
		NextProtos:   nextProtos,
		MinVersion:   tls.VersionTLS12,
	}
}

// mustCert returns the package certificate or fails the test.
func mustCert(tb testing.TB) certBundle {
	tb.Helper()
	cert, err := testCert()
	if err != nil {
		tb.Fatal(err)
	}
	return cert
}
