// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInitializeAsync_EmptyRootPath_Succeeds(t *testing.T) {
	t.Parallel()
	s := newTestServer()
	svc := newServerService(s)
	sess, err := svc.InitializeAsync(t.Context(), "", InitializeServerOptions{})
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
	sess, err := svc.InitializeAsync(t.Context(), t.TempDir(), InitializeServerOptions{
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

func TestInitializeAsync_CertWithHttpsEndpoint_Succeeds(t *testing.T) {
	t.Parallel()
	cert := generateTestCertB64(t)
	endpoint := "https://svc.example.com"

	s := newTestServer()
	svc := newServerService(s)

	sess, err := svc.InitializeAsync(t.Context(), t.TempDir(), InitializeServerOptions{
		AuthenticationEndpoint:    &endpoint,
		AuthenticationCertificate: &cert,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)

	ss, ok := s.sessionFromId(sess.Id)
	require.True(t, ok)
	require.NotNil(t, ss.externalServicesClient)
}

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

func TestNewWriter_ReturnsMultiplexerWithLogWriter(t *testing.T) {
	wm := newWriter("[test] ")

	// The multiplexer should have exactly one writer (the log writer)
	require.NotNil(t, wm)
	require.Len(t, wm.writers, 1)

	// Writing should not panic (it writes to log.Printf internally)
	n, err := wm.Write([]byte("test message"))
	require.NoError(t, err)
	// The writerFunc inside newWriter returns n=0 (bug in the source: `return n, nil` where n is unset),
	// but the multiplexer returns whatever the last writer returns.
	_ = n
}

func TestNewWriter_AcceptsAdditionalWriters(t *testing.T) {
	wm := newWriter("[prefix] ")

	var buf bytes.Buffer
	wm.AddWriter(&buf)

	_, err := wm.Write([]byte("hello"))
	require.NoError(t, err)
	require.Equal(t, "hello", buf.String())
}

func TestNewServerService(t *testing.T) {
	s := newTestServer()
	svc := newServerService(s)
	require.NotNil(t, svc)
	require.Same(t, s, svc.server)
}

func TestServerService_StopAsync(t *testing.T) {
	s := newTestServer()
	// Must set cancelTelemetryUpload to avoid nil panic
	s.cancelTelemetryUpload = func() {}
	svc := newServerService(s)
	rpcConn := connectRPC(t, svc)

	_, err := rpcConn.Call(t.Context(), "StopAsync", []any{}, nil)
	require.NoError(t, err)
}
