// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package cmd

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

// testPipeName returns a pipe name unique to this process and call site.
func testPipeName(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("azd-auth-test-%d-%d", os.Getpid(), time.Now().UnixNano())
}

// listenSecurePipe creates a named pipe restricted to the current user.
func listenSecurePipe(t *testing.T, pipePath string) net.Listener {
	t.Helper()
	sid, err := currentProcessUserSid()
	require.NoError(t, err)

	return listenPipeWithSecurityDescriptor(t, pipePath, fmt.Sprintf("D:P(A;;GA;;;%s)", sid.String()))
}

func listenPipeWithSecurityDescriptor(t *testing.T, pipePath, securityDescriptor string) net.Listener {
	t.Helper()
	l, err := winio.ListenPipe(pipePath, &winio.PipeConfig{
		SecurityDescriptor: securityDescriptor,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func verifyConnectedPipeSecurity(t *testing.T, pipePath string, l net.Listener) error {
	t.Helper()

	go func() {
		if c, err := l.Accept(); err == nil {
			_ = c.Close()
		}
	}()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	conn, err := winio.DialPipeContext(ctx, pipePath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	handle, err := pipeConnHandle(conn)
	require.NoError(t, err)
	require.NotEqual(t, windows.Handle(0), handle)

	return verifyPipeSecurity(pipePath, handle)
}

func TestNormalizePipePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "short form opaque", in: "npipe:azd-auth-foo", want: `\\.\pipe\azd-auth-foo`},
		{name: "short form path", in: "npipe:/azd-auth-foo", want: `\\.\pipe\azd-auth-foo`},
		{name: "qualified authority form", in: "npipe://./pipe/azd-auth-foo", want: `\\.\pipe\azd-auth-foo`},
		{name: "qualified path form", in: "npipe:////./pipe/azd-auth-foo", want: `\\.\pipe\azd-auth-foo`},
		{name: "backslash namespace", in: `npipe:azd-auth\nested`, want: `\\.\pipe\azd-auth\nested`},
		{name: "missing short name", in: "npipe:", wantErr: true},
		{name: "missing qualified authority name", in: "npipe://./pipe/", wantErr: true},
		{name: "missing qualified path name", in: "npipe:////./pipe/", wantErr: true},
		{name: "remote authority", in: "npipe://server/pipe/azd-auth-foo", wantErr: true},
		{name: "unexpected local path", in: "npipe://./other/azd-auth-foo", wantErr: true},
		{name: "forward slash in name", in: "npipe:azd-auth/foo", wantErr: true},
		{name: "malformed qualified path", in: "npipe:///pipe/azd-auth-foo", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizePipePath(tt.in)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestNewSocketTransport_NotSupportedOnWindows(t *testing.T) {
	t.Parallel()
	_, _, err := newSocketTransport("unix:/tmp/x.sock")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported on this platform")
}

func TestVerifyPipeOwner(t *testing.T) {
	t.Parallel()

	currentUserSid, err := currentProcessUserSid()
	require.NoError(t, err)
	systemSid, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	require.NoError(t, err)
	adminsSid, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	require.NoError(t, err)
	worldSid, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	require.NoError(t, err)
	allowedSids := []*windows.SID{currentUserSid, systemSid, adminsSid}

	tests := []struct {
		name       string
		owner      *windows.SID
		wantErrSub string
	}{
		{name: "current user", owner: currentUserSid},
		{name: "system", owner: systemSid},
		{name: "administrators", owner: adminsSid},
		{name: "missing owner", wantErrSub: "has no owner SID"},
		{name: "untrusted owner", owner: worldSid, wantErrSub: "outside the current user/SYSTEM/Administrators"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyPipeOwner(`\\.\pipe\azd-auth-test`, tt.owner, allowedSids)
			if tt.wantErrSub == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tt.wantErrSub)
			}
		})
	}
}

// TestPipeConnHandle_UnsupportedConn asserts that a connection which does not
// expose a Win32 handle is refused rather than silently skipping verification.
func TestPipeConnHandle_UnsupportedConn(t *testing.T) {
	t.Parallel()
	c1, c2 := net.Pipe()
	t.Cleanup(func() { _ = c1.Close(); _ = c2.Close() })

	_, err := pipeConnHandle(c1)
	require.ErrorContains(t, err, "does not expose its handle")
}

// TestVerifyPipeSecurity_ConnectedHandle verifies that a restrictive pipe
// passes verification when checked through the established connection handle.
func TestVerifyPipeSecurity_ConnectedHandle(t *testing.T) {
	name := testPipeName(t)
	pipePath := `\\.\pipe\` + name

	l := listenSecurePipe(t, pipePath)

	require.NoError(t, verifyConnectedPipeSecurity(t, pipePath, l))
}

// TestVerifyPipeSecurity_AcceptsDefaultDACL verifies compatibility with Node
// and other hosts that use the Windows default named pipe security descriptor.
// Its Everyone and Anonymous ACEs grant read access but cannot be used to send
// a token request or observe another client's pipe instance.
func TestVerifyPipeSecurity_AcceptsDefaultDACL(t *testing.T) {
	name := testPipeName(t)
	pipePath := `\\.\pipe\` + name

	l, err := winio.ListenPipe(pipePath, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	require.NoError(t, verifyConnectedPipeSecurity(t, pipePath, l))
}

func TestVerifyPipeSecurity_RejectsUnsafeAllowACEs(t *testing.T) {
	currentUserSid, err := currentProcessUserSid()
	require.NoError(t, err)

	tests := []struct {
		name       string
		access     string
		sid        string
		wantErrSub string
	}{
		{
			name:       "everyone write",
			access:     "GW",
			sid:        "WD",
			wantErrSub: "grants non-read-only access mask",
		},
		{
			name:       "anonymous write",
			access:     "GW",
			sid:        "AN",
			wantErrSub: "grants non-read-only access mask",
		},
		{
			name:       "another group read",
			access:     "GR",
			sid:        "AU",
			wantErrSub: "outside the current user/SYSTEM/Administrators/Everyone/Anonymous policy",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipePath := `\\.\pipe\` + testPipeName(t)
			descriptor := fmt.Sprintf(
				"D:P(A;;GA;;;%s)(A;;%s;;;%s)",
				currentUserSid.String(),
				tt.access,
				tt.sid)
			l := listenPipeWithSecurityDescriptor(t, pipePath, descriptor)

			err := verifyConnectedPipeSecurity(t, pipePath, l)
			require.ErrorContains(t, err, tt.wantErrSub)
		})
	}
}

// TestNewPipeTransport_FullRoundTrip serves HTTP over a named pipe and checks
// that the transport built by newPipeTransport connects, passes security
// verification, and reaches the handler.
func TestNewPipeTransport_FullRoundTrip(t *testing.T) {
	name := testPipeName(t)

	pipePath := `\\.\pipe\` + name
	l, err := winio.ListenPipe(pipePath, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })

	srv := &http.Server{
		ReadHeaderTimeout: 30 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.URL.Path, "/token") {
				http.Error(w, "unexpected path", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w,
				`{"status":"success","token":"tok","expiresOn":"2099-01-01T00:00:00Z"}`)
		}),
	}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })

	rt, endpoint, err := newPipeTransport("npipe://./pipe/" + name)
	require.NoError(t, err)
	require.Equal(t, rewrittenAuthEndpoint, endpoint)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, endpoint+"/token?api-version=2023-07-12-preview", nil)
	require.NoError(t, err)

	resp, err := (&http.Client{Transport: rt}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), `"token":"tok"`)
}
