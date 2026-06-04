// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build unix

package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"syscall"
)

// newSocketTransport builds an http.RoundTripper that dispatches requests over
// the Unix domain socket identified by rawURL. The returned string is the
// rewritten endpoint placeholder that should be used in
// auth.ExternalAuthConfiguration.Endpoint.
//
// The socket file and its parent directory MUST be owned by the current uid
// and have group/other bits cleared (mode & 0o077 == 0). If either check
// fails, an error is returned and no transport is constructed.
func newSocketTransport(rawURL string) (http.RoundTripper, string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, "", fmt.Errorf("invalid AZD_AUTH_ENDPOINT value %q: %w", rawURL, err)
	}
	if u.Scheme != "unix" {
		return nil, "", fmt.Errorf("internal error: newSocketTransport called with non-unix scheme %q", u.Scheme)
	}

	socketPath := u.Path
	if socketPath == "" {
		// url.Parse puts host of "unix:/foo" into Path but "unix://foo" puts
		// "foo" into Host; fall back to Host when Path is empty.
		socketPath = u.Host
	}
	if !filepath.IsAbs(socketPath) {
		return nil, "", fmt.Errorf(
			"invalid AZD_AUTH_ENDPOINT value %q: unix scheme requires an absolute socket path", rawURL)
	}

	if err := verifySocketPermissions(socketPath); err != nil {
		return nil, "", err
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return transport, rewrittenAuthEndpoint, nil
}

// verifySocketPermissions checks that the socket file and its parent
// directory are owned by the current effective uid and have group/other
// permission bits cleared. It returns a clear error when either check fails.
func verifySocketPermissions(socketPath string) error {
	parent := filepath.Dir(socketPath)
	if err := checkPathOwnedAndRestricted(parent, true); err != nil {
		return fmt.Errorf("AZD_AUTH_ENDPOINT socket parent directory %q: %w", parent, err)
	}
	if err := checkPathOwnedAndRestricted(socketPath, false); err != nil {
		return fmt.Errorf("AZD_AUTH_ENDPOINT socket %q: %w", socketPath, err)
	}
	return nil
}

// checkPathOwnedAndRestricted verifies path is owned by the current euid and
// has mode bits group/other set to zero. The isDir flag is used only for
// error messages.
func checkPathOwnedAndRestricted(path string, isDir bool) error {
	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		return fmt.Errorf("path must be absolute")
	}

	info, err := os.Stat(cleanPath)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("unable to read ownership information")
	}
	euid := os.Geteuid()
	if int64(sys.Uid) != int64(euid) {
		kind := "file"
		if isDir {
			kind = "directory"
		}
		return fmt.Errorf("permissions too permissive: %s owner uid %d does not match current euid %d",
			kind, sys.Uid, euid)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf(
			"permissions too permissive: mode %#o grants access beyond owner (group/world bits must be 0)",
			info.Mode().Perm())
	}
	return nil
}

// newPipeTransport returns an error: named pipes are Windows-only. This stub
// exists so container.go can call it portably.
func newPipeTransport(rawURL string) (http.RoundTripper, string, error) {
	return nil, "", fmt.Errorf(
		"AZD_AUTH_ENDPOINT scheme 'npipe' is not supported on this platform; use 'unix' or 'https'")
}
