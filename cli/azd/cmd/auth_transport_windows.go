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
// The pipe's security descriptor MUST be owned by the current user SID or the
// conventional SYSTEM / Administrators principals. Allow ACEs may grant those
// principals any access. The standard Everyone / Anonymous read-only ACEs are
// also accepted, but any other SID or broader access is refused.
//
// Verification is performed against the handle of the established connection
// rather than the pipe name, so a pipe that is created or replaced between the
// connect and the check cannot be substituted for the validated one.
func newPipeTransport(rawURL string) (http.RoundTripper, string, error) {
	pipePath, err := normalizePipePath(rawURL)
	if err != nil {
		return nil, "", err
	}

	transport := &http.Transport{
		Proxy: nil, // Local IPC must not honor proxy environment variables.
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			conn, err := winio.DialPipeContext(ctx, pipePath)
			if err != nil {
				return nil, err
			}
			handle, hErr := pipeConnHandle(conn)
			if hErr != nil {
				_ = conn.Close()
				return nil, hErr
			}
			if vErr := verifyPipeSecurity(pipePath, handle); vErr != nil {
				_ = conn.Close()
				return nil, vErr
			}
			return conn, nil
		},
	}
	return transport, rewrittenAuthEndpoint, nil
}

// pipeConnHandle extracts the underlying Win32 handle from a named pipe
// connection. go-winio's pipe connection exposes the handle through an
// exported Fd method; it does not implement syscall.Conn. The handle is only
// used while conn is still owned by the caller, before it is handed to the
// transport, so it cannot be closed concurrently.
func pipeConnHandle(conn net.Conn) (windows.Handle, error) {
	fdConn, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return 0, fmt.Errorf(
			"verifying AZD_AUTH_ENDPOINT pipe: connection type %T does not expose its handle", conn)
	}
	return windows.Handle(fdConn.Fd()), nil
}

// newSocketTransport returns an error: unix domain sockets are not supported
// on Windows.
func newSocketTransport(rawURL string) (http.RoundTripper, string, error) {
	return nil, "", fmt.Errorf(
		"AZD_AUTH_ENDPOINT scheme 'unix' is not supported on this platform; use 'npipe' or 'https'")
}

// normalizePipePath accepts the short forms `npipe:azd-auth-...` and
// `npipe:/azd-auth-...`, or the fully qualified forms
// `npipe://./pipe/azd-auth-...` and `npipe:////./pipe/azd-auth-...`, and
// returns a fully qualified pipe path of the form `\\.\pipe\<name>`.
func normalizePipePath(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid AZD_AUTH_ENDPOINT value %q: %w", rawURL, err)
	}
	if u.Scheme != "npipe" {
		return "", fmt.Errorf("internal error: normalizePipePath called with non-npipe scheme %q", u.Scheme)
	}

	var name string
	switch {
	// Fully qualified authority form:
	// npipe://./pipe/<name> -> Host=".", Path="/pipe/<name>"
	case u.Host == ".":
		if u.User != nil || !strings.HasPrefix(u.Path, "/pipe/") {
			return "", fmt.Errorf("invalid AZD_AUTH_ENDPOINT value %q: expected local pipe path /pipe/<name>", rawURL)
		}
		name = strings.TrimPrefix(u.Path, "/pipe/")
	case u.Host != "":
		return "", fmt.Errorf("invalid AZD_AUTH_ENDPOINT value %q: named pipe authority must be local", rawURL)
	// Fully qualified path form:
	// npipe:////./pipe/<name> -> Host="", Path="//./pipe/<name>"
	case strings.HasPrefix(u.Path, "//./pipe/"):
		name = strings.TrimPrefix(u.Path, "//./pipe/")
	case strings.HasPrefix(u.Path, "//"):
		return "", fmt.Errorf("invalid AZD_AUTH_ENDPOINT value %q: expected local pipe path //./pipe/<name>", rawURL)
	// Short form: npipe:<name> -> Opaque="<name>"
	case u.Opaque != "":
		name = u.Opaque
	// Fallback short form: npipe:/<name>
	default:
		name = strings.TrimPrefix(u.Path, "/")
	}
	if name == "" {
		return "", fmt.Errorf("invalid AZD_AUTH_ENDPOINT value %q: missing pipe name", rawURL)
	}
	if strings.Contains(name, "/") {
		return "", fmt.Errorf("invalid AZD_AUTH_ENDPOINT value %q: pipe name must not contain '/'", rawURL)
	}
	return `\\.\pipe\` + name, nil
}

// verifyPipeSecurity queries the owner and DACL of the connected named pipe
// handle. It permits full access for the current user / SYSTEM / Administrators
// and read-only access for Everyone / Anonymous. ACEs are walked structurally
// via windows.GetAce rather than by parsing the SDDL string representation.
//
// The security descriptor is read from the connection handle rather than by
// pipe name so that the object being validated is exactly the object being
// used. pipePath is used only for error messages.
func verifyPipeSecurity(pipePath string, handle windows.Handle) error {
	sd, err := windows.GetSecurityInfo(
		handle,
		windows.SE_KERNEL_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION,
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
	worldSid, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	if err != nil {
		return fmt.Errorf("creating Everyone SID: %w", err)
	}
	anonymousSid, err := windows.CreateWellKnownSid(windows.WinAnonymousSid)
	if err != nil {
		return fmt.Errorf("creating Anonymous SID: %w", err)
	}
	readOnlySids := []*windows.SID{worldSid, anonymousSid}

	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("reading pipe owner: %w", err)
	}
	if err := verifyPipeOwner(pipePath, owner, allowedSids); err != nil {
		return err
	}

	for i := range dacl.AceCount {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
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
			sid, err := accessAllowedAceSid(ace)
			if err != nil {
				return fmt.Errorf("reading SID from ACE %d: %w", i, err)
			}
			switch {
			case sidInList(sid, allowedSids):
				continue
			case sidInList(sid, readOnlySids):
				if ace.Mask&^readOnlyPipeAccessMask != 0 {
					return fmt.Errorf(
						"permissions too permissive: pipe %q grants non-read-only access mask %#x to SID %q",
						pipePath, uint32(ace.Mask), sid.String())
				}
			default:
				return fmt.Errorf(
					"permissions too permissive: pipe %q grants access to SID %q "+
						"outside the current user/SYSTEM/Administrators/Everyone/Anonymous policy",
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

func verifyPipeOwner(pipePath string, owner *windows.SID, allowedSids []*windows.SID) error {
	if owner == nil {
		return fmt.Errorf("permissions too permissive: pipe %q has no owner SID", pipePath)
	}
	if !sidInList(owner, allowedSids) {
		return fmt.Errorf(
			"permissions too permissive: pipe %q is owned by SID %q "+
				"outside the current user/SYSTEM/Administrators",
			pipePath, owner.String())
	}
	return nil
}

// AceType constants not (yet) exposed by golang.org/x/sys/windows.
// See https://learn.microsoft.com/windows/win32/api/winnt/ns-winnt-ace_header.
const (
	accessAllowedObjectAceType         uint8 = 0x05
	accessAllowedCallbackAceType       uint8 = 0x09
	accessAllowedCallbackObjectAceType uint8 = 0x0B
	readOnlyPipeAccessMask                   = windows.GENERIC_READ | windows.FILE_GENERIC_READ
)

func accessAllowedAceSid(ace *windows.ACCESS_ALLOWED_ACE) (*windows.SID, error) {
	//nolint:gosec // Win32 ACCESS_ALLOWED_ACE stores the SID inline at SidStart.
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !sid.IsValid() {
		return nil, fmt.Errorf("invalid SID in access-allowed ACE type %d", ace.Header.AceType)
	}
	return sid, nil
}

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
