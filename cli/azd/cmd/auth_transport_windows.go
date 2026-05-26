// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build windows

package cmd

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/user"
	"regexp"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

// newPipeTransport builds an http.RoundTripper that dispatches requests over
// the Windows named pipe identified by rawURL. The returned string is the
// rewritten endpoint placeholder.
//
// The pipe's security descriptor MUST grant access only to the current user
// SID, plus the conventional SYSTEM / Administrators principals. If any other
// SID has an allow ACE, an error is returned.
func newPipeTransport(rawURL string) (http.RoundTripper, string, error) {
	pipePath, err := normalizePipePath(rawURL)
	if err != nil {
		return nil, "", err
	}

	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := winio.DialPipeContext(ctx, pipePath)
			if err != nil {
				return nil, err
			}
			if verr := verifyPipeSecurity(pipePath); verr != nil {
				_ = conn.Close()
				return nil, verr
			}
			return conn, nil
		},
	}
	return transport, rewrittenAuthEndpoint, nil
}

// newSocketTransport returns an error: unix domain sockets are not supported
// on Windows.
func newSocketTransport(rawURL string) (http.RoundTripper, string, error) {
	return nil, "", fmt.Errorf(
		"AZD_AUTH_ENDPOINT scheme 'unix' is not supported on this platform; use 'npipe' or 'https'")
}

// normalizePipePath accepts either short form `npipe:azd-auth-...` or long
// form `npipe:////./pipe/azd-auth-...` and returns a fully qualified pipe
// path of the form `\\.\pipe\<name>`.
func normalizePipePath(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid AZD_AUTH_ENDPOINT value %q: %w", rawURL, err)
	}
	if u.Scheme != "npipe" {
		return "", fmt.Errorf("internal error: normalizePipePath called with non-npipe scheme %q", u.Scheme)
	}

	// Long form: npipe:////./pipe/<name>  -> Host="." Path="/pipe/<name>"
	if u.Host == "." && strings.HasPrefix(u.Path, "/pipe/") {
		name := strings.TrimPrefix(u.Path, "/pipe/")
		if name == "" {
			return "", fmt.Errorf("invalid AZD_AUTH_ENDPOINT value %q: missing pipe name", rawURL)
		}
		return `\\.\pipe\` + name, nil
	}

	// Short form: npipe:<name> -> Opaque="<name>"
	if u.Opaque != "" {
		return `\\.\pipe\` + u.Opaque, nil
	}

	// Fallback: short form without colon-opaque, e.g. npipe:/<name>
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		return "", fmt.Errorf("invalid AZD_AUTH_ENDPOINT value %q: missing pipe name", rawURL)
	}
	return `\\.\pipe\` + name, nil
}

// sddlAceSidRE captures the trailing SID of each ACE in an SDDL DACL string.
// An ACE has the form "(type;flags;rights;object;inherit_object;account_sid)".
// We only care about the account_sid component, which appears after the last
// semicolon and before the closing parenthesis.
var sddlAceSidRE = regexp.MustCompile(`\(([^)]*)\)`)

// verifyPipeSecurity queries the DACL of the named pipe and refuses if any
// allow ACE references a SID outside the current user / SYSTEM /
// Administrators set.
func verifyPipeSecurity(pipePath string) error {
	sd, err := windows.GetNamedSecurityInfo(
		pipePath,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("querying pipe security descriptor: %w", err)
	}

	// Render the DACL as SDDL so we can enumerate ACEs without taking a
	// direct dependency on raw Win32 ACE parsing.
	sddl := sd.String()

	cur, err := user.Current()
	if err != nil {
		return fmt.Errorf("looking up current user: %w", err)
	}
	allowedSids := map[string]struct{}{
		strings.ToUpper(cur.Uid): {},
	}
	// Well-known SIDs that are always acceptable per spec.
	for _, wk := range []windows.WELL_KNOWN_SID_TYPE{
		windows.WinLocalSystemSid,
		windows.WinBuiltinAdministratorsSid,
	} {
		s, err := windows.CreateWellKnownSid(wk)
		if err == nil {
			allowedSids[strings.ToUpper(s.String())] = struct{}{}
		}
	}
	// SDDL short forms for SYSTEM ("SY"), Administrators ("BA"), and Local
	// Administrators ("LA") are also acceptable.
	for _, short := range []string{"SY", "BA", "LA"} {
		allowedSids[short] = struct{}{}
	}

	// Extract the DACL substring. An SDDL is of the form
	// "O:<owner>G:<group>D:<dacl>S:<sacl>"; the D: section contains the ACEs
	// we care about.
	daclIdx := strings.Index(sddl, "D:")
	if daclIdx < 0 {
		// No DACL section at all means the DACL was NULL (full access for
		// everyone) — refuse.
		return fmt.Errorf("permissions too permissive: pipe %q has no DACL", pipePath)
	}
	daclStr := sddl[daclIdx:]
	if sIdx := strings.Index(daclStr, "S:"); sIdx >= 0 {
		daclStr = daclStr[:sIdx]
	}

	for _, m := range sddlAceSidRE.FindAllStringSubmatch(daclStr, -1) {
		parts := strings.Split(m[1], ";")
		if len(parts) < 6 {
			continue
		}
		aceType := strings.ToUpper(strings.TrimSpace(parts[0]))
		// Only ACCESS_ALLOWED_ACE ("A") and ACCESS_ALLOWED_OBJECT_ACE ("OA")
		// grant access; the spec is concerned with allow ACEs.
		if aceType != "A" && aceType != "OA" {
			continue
		}
		sidStr := strings.ToUpper(strings.TrimSpace(parts[5]))
		if _, ok := allowedSids[sidStr]; ok {
			continue
		}
		return fmt.Errorf(
			"permissions too permissive: pipe %q grants access to SID %q outside the current user/SYSTEM/Administrators",
			pipePath, parts[5])
	}
	return nil
}
