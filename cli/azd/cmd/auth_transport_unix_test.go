// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build unix

package cmd

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// listenUnixSocket creates a UDS at <dir>/azd.sock with mode 0600 inside a
// 0700 parent directory and returns the socket path and the listener. The
// listener is closed automatically.
func listenUnixSocket(t *testing.T) (string, net.Listener) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o700))
	sock := filepath.Join(dir, "azd.sock")
	l, err := net.Listen("unix", sock)
	require.NoError(t, err)
	// net.Listen creates the socket with the process umask; force 0600 for
	// determinism.
	require.NoError(t, os.Chmod(sock, 0o600))
	t.Cleanup(func() { _ = l.Close() })
	return sock, l
}

func TestNewSocketTransport_RejectsRelativePath(t *testing.T) {
	t.Parallel()
	_, _, err := newSocketTransport("unix:relative/socket")
	require.Error(t, err)
	require.Contains(t, err.Error(), "absolute socket path")
}

func TestNewSocketTransport_OverlyPermissiveParent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o755))
	sock := filepath.Join(dir, "azd.sock")
	// Create an empty file to stand in for the socket; the permission check
	// happens before any connect attempt.
	require.NoError(t, os.WriteFile(sock, nil, 0o600))

	_, _, err := newSocketTransport("unix:" + sock)
	require.Error(t, err)
	require.Contains(t, err.Error(), "permissions too permissive")
}

func TestNewSocketTransport_OverlyPermissiveSocket(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o700))
	sock := filepath.Join(dir, "azd.sock")
	require.NoError(t, os.WriteFile(sock, nil, 0o644))

	_, _, err := newSocketTransport("unix:" + sock)
	require.Error(t, err)
	require.Contains(t, err.Error(), "permissions too permissive")
}

// TestNewSocketTransport_FullRoundTrip starts an httptest.Server backed by a
// UDS listener and verifies that newSocketTransport produces a transport that
// can reach the server and receive a token-shaped response.
func TestNewSocketTransport_FullRoundTrip(t *testing.T) {
	sock, l := listenUnixSocket(t)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The endpoint placeholder is "http://azd-auth"; the request path
		// must remain "/token" with the api-version query.
		if !strings.HasPrefix(r.URL.Path, "/token") {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w,
			`{"status":"success","token":"tok","expiresOn":"2099-01-01T00:00:00Z"}`)
	}))
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)

	rt, endpoint, err := newSocketTransport("unix:" + sock)
	require.NoError(t, err)
	require.Equal(t, rewrittenAuthEndpoint, endpoint)

	client := &http.Client{Transport: rt}
	resp, err := client.Get(endpoint + "/token?api-version=2023-07-12-preview")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), `"token":"tok"`)
}

// TestNewSocketTransport_HappyPath verifies that a properly-secured socket
// produces a transport without error (independent of an actual server).
func TestNewSocketTransport_HappyPath(t *testing.T) {
	sock, _ := listenUnixSocket(t)
	rt, endpoint, err := newSocketTransport("unix:" + sock)
	require.NoError(t, err)
	require.NotNil(t, rt)
	require.Equal(t, rewrittenAuthEndpoint, endpoint)
}

// TestNewPipeTransport_NotSupportedOnUnix asserts the npipe stub returns a
// clear error on POSIX.
func TestNewPipeTransport_NotSupportedOnUnix(t *testing.T) {
	t.Parallel()
	_, _, err := newPipeTransport("npipe:azd-auth-x")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported on this platform")
}
