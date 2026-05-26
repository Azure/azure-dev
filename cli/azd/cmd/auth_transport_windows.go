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
	"strings"
	"unsafe"

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

// verifyPipeSecurity queries the DACL of the named pipe and refuses if any
// allow ACE references a SID outside the current user / SYSTEM /
// Administrators set. ACEs are walked structurally via windows.GetAce rather
// than by parsing the SDDL string representation.
func verifyPipeSecurity(pipePath string) error {
	sd, err := windows.GetNamedSecurityInfo(
		pipePath,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("querying pipe security descriptor: %w", err)
	}

	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("reading DACL: %w", err)
	}
	// A nil DACL means full access for everyone — refuse.
	if dacl == nil {
		return fmt.Errorf("permissions too permissive: pipe %q has a NULL DACL", pipePath)
	}

	currentUserSid, err := currentProcessUserSid()
	if err != nil {
		return fmt.Errorf("looking up current user SID: %w", err)
	}
	systemSid, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return fmt.Errorf("creating SYSTEM SID: %w", err)
	}
	adminsSid, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return fmt.Errorf("creating Administrators SID: %w", err)
	}
	allowedSids := []*windows.SID{currentUserSid, systemSid, adminsSid}

	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("reading ACE %d: %w", i, err)
		}
		// For ACCESS_ALLOWED_ACE_TYPE and its callback variant, the ACE
		// layout starts with ACE_HEADER + ACCESS_MASK and is immediately
		// followed by the SID in place; ACCESS_ALLOWED_ACE.SidStart marks
		// that first SID byte. The unsafe cast below is only valid for
		// those two AceType values — object ACEs interleave GUID fields
		// before the SID and are handled separately.
		switch ace.Header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE, accessAllowedCallbackAceType:
			sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
			if !sidInList(sid, allowedSids) {
				return fmt.Errorf(
					"permissions too permissive: pipe %q grants access to SID %q "+
						"outside the current user/SYSTEM/Administrators",
					pipePath, sid.String())
			}
		case accessAllowedObjectAceType, accessAllowedCallbackObjectAceType:
			return fmt.Errorf(
				"permissions too permissive: pipe %q has an Active Directory-style object "+
					"allow ACE which is not expected on a named pipe",
				pipePath)
		default:
			// Deny / audit / other ACE types do not grant access; skip.
		}
	}
	return nil
}

// AceType constants not (yet) exposed by golang.org/x/sys/windows.
// See https://learn.microsoft.com/windows/win32/api/winnt/ns-winnt-ace_header.
const (
	accessAllowedObjectAceType         uint8 = 0x05
	accessAllowedCallbackAceType       uint8 = 0x09
	accessAllowedCallbackObjectAceType uint8 = 0x0B
)

// currentProcessUserSid returns the SID of the user owning the current
// process token. This is preferred over user.Current() because it avoids a
// roundtrip through string parsing and reflects the actual access token.
func currentProcessUserSid() (*windows.SID, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(
		windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return nil, err
	}
	defer token.Close()
	tu, err := token.GetTokenUser()
	if err != nil {
		return nil, err
	}
	// Copy the SID off the token-owned buffer so it remains valid after
	// token.Close().
	return tu.User.Sid.Copy()
}

func sidInList(sid *windows.SID, list []*windows.SID) bool {
	for _, s := range list {
		if windows.EqualSid(sid, s) {
			return true
		}
	}
	return false
}
