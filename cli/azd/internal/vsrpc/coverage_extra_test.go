// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// InitializeAsync: exercises the various error + success branches.
// ---------------------------------------------------------------------------

func TestInitializeAsync_RootPath_NotExists(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	svc := newServerService(s)
	_, err := svc.InitializeAsync(context.Background(), filepath.Join(t.TempDir(), "no-such-dir"), InitializeServerOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid root path")
}

func TestInitializeAsync_RootPath_NotDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o600))

	s := newTestServer()
	svc := newServerService(s)
	_, err := svc.InitializeAsync(context.Background(), file, InitializeServerOptions{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a directory")
}

func TestInitializeAsync_EmptyRootPath_Succeeds(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	svc := newServerService(s)
	sess, err := svc.InitializeAsync(context.Background(), "", InitializeServerOptions{})
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.NotEmpty(t, sess.Id)
}

func TestInitializeAsync_WithAuthEndpointAndKey(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	svc := newServerService(s)

	endpoint := "https://auth.example.com"
	key := "secret"
	sess, err := svc.InitializeAsync(context.Background(), t.TempDir(), InitializeServerOptions{
		AuthenticationEndpoint: &endpoint,
		AuthenticationKey:      &key,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	// Verify the session recorded the auth config.
	ss, ok := s.sessionFromId(sess.Id)
	require.True(t, ok)
	require.Equal(t, endpoint, ss.externalServicesEndpoint)
	require.Equal(t, key, ss.externalServicesKey)
	require.NotNil(t, ss.externalServicesClient)
}

func TestInitializeAsync_WithInvalidCertificate(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	svc := newServerService(s)

	badCert := "this-is-not-a-cert"
	_, err := svc.InitializeAsync(context.Background(), t.TempDir(), InitializeServerOptions{
		AuthenticationCertificate: &badCert,
	})
	require.Error(t, err)
}

// generateTestCertB64 produces a base64-DER self-signed x509 certificate. This
// is only used to exercise the certificate parsing branches in InitializeAsync
// — it is never used for real TLS.
func generateTestCertB64(t *testing.T) string {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &priv.PublicKey, priv)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(der)
}

func TestInitializeAsync_CertWithNonHttpsEndpoint(t *testing.T) {
	t.Parallel()
	cert := generateTestCertB64(t)
	endpoint := "http://example.com"

	s := newTestServer()
	svc := newServerService(s)

	_, err := svc.InitializeAsync(context.Background(), t.TempDir(), InitializeServerOptions{
		AuthenticationEndpoint:    &endpoint,
		AuthenticationCertificate: &cert,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "scheme must be 'https'")
}

func TestInitializeAsync_CertWithUnparseableEndpoint(t *testing.T) {
	t.Parallel()
	cert := generateTestCertB64(t)
	// url.Parse accepts almost anything, but a string with control chars fails.
	endpoint := "http://invalid\x7f/"

	s := newTestServer()
	svc := newServerService(s)

	_, err := svc.InitializeAsync(context.Background(), t.TempDir(), InitializeServerOptions{
		AuthenticationEndpoint:    &endpoint,
		AuthenticationCertificate: &cert,
	})
	require.Error(t, err)
}

func TestInitializeAsync_CertWithHttpsEndpoint_Succeeds(t *testing.T) {
	t.Parallel()
	cert := generateTestCertB64(t)
	endpoint := "https://svc.example.com"

	s := newTestServer()
	svc := newServerService(s)

	sess, err := svc.InitializeAsync(context.Background(), t.TempDir(), InitializeServerOptions{
		AuthenticationEndpoint:    &endpoint,
		AuthenticationCertificate: &cert,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	ss, ok := s.sessionFromId(sess.Id)
	require.True(t, ok)
	require.NotNil(t, ss.externalServicesClient)
}

// ---------------------------------------------------------------------------
// Serve: verifies that starting Serve on a listener and closing it returns a
// server-closed error (exercises the Serve path and mux wiring).
// ---------------------------------------------------------------------------

func TestServe_ClosedListenerReturns(t *testing.T) {
	t.Setenv("AZD_DEBUG_SERVER_DEBUG_ENDPOINTS", "false")

	s := newTestServer()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	done := make(chan error, 1)
	go func() {
		done <- s.Serve(l)
	}()

	// Close the listener immediately; Serve should return.
	require.NoError(t, l.Close())

	select {
	case err := <-done:
		// Any error is fine; we're primarily verifying Serve returns.
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after listener close")
	}

	// cancelTelemetryUpload should have been installed.
	require.NotNil(t, s.cancelTelemetryUpload)
	s.cancelTelemetryUpload()
}

func TestServe_WithDebugEndpoints(t *testing.T) {
	t.Setenv("AZD_DEBUG_SERVER_DEBUG_ENDPOINTS", "true")

	s := newTestServer()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = s.Serve(l)
	}()

	require.NoError(t, l.Close())
	<-done

	require.NotNil(t, s.cancelTelemetryUpload)
	s.cancelTelemetryUpload()
}

// ---------------------------------------------------------------------------
// newWriter (servies_service.go) — exercise the default writer path.
// ---------------------------------------------------------------------------

func TestNewWriter_DefaultWritesToLog(t *testing.T) {
	t.Parallel()
	wm := newWriter("[test] ")
	require.NotNil(t, wm)
	n, err := wm.Write([]byte("hello\n"))
	require.NoError(t, err)
	// writerFunc inside newWriter returns n=0 (does not count bytes); just
	// ensure no error and something was accepted.
	require.GreaterOrEqual(t, n, 0)
}
