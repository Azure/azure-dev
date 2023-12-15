// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build broker && windows

package oneauth

/*
#include <stdbool.h>
#include <stdlib.h>

// This must match the definition in bridge.h exactly. We don't include
// bridge.h because doing so would make the bridge DLL a dependency of
// azd.exe and prevent distributing the DLL via embedding because Windows
// won't execute a program's entry point if its DLL dependencies are
// unavailable.
typedef struct
{
	char *accountID;
	char *errorDescription;
	int expiresOn;
	char *loginName;
	char *token;
} AuthnResult;
*/
import "C"

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/azure/azure-dev/cli/azd/internal"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"golang.org/x/sys/windows"
)

const (
	applicationID = "com.microsoft.azd"

	// Supported indicates whether this build supports brokered authentication.
	Supported = true
)

var (
	//go:embed bridge/_build/Release/bridge.dll
	bridgeDLL []byte
	//go:embed bridge/_build/Release/bridge.dll.sha256
	bridgeChecksum []byte
	//go:embed bridge/_build/Release/fmt.dll
	fmtDLL []byte
	//go:embed bridge/_build/Release/fmt.dll.sha256
	fmtChecksum []byte

	// bridge is a lazy-loaded DLL that provides access to the OneAuth API
	bridge       *windows.LazyDLL
	authenticate *windows.LazyProc
	freeAR       *windows.LazyProc
	logout       *windows.LazyProc
	shutdown     *windows.LazyProc
	startup      *windows.LazyProc

	// started tracks whether the bridge's Startup function has succeeded. This is necessary
	// because OneAuth returns an error when its Startup function is called more than once.
	started atomic.Bool
)

type authResult struct {
	errorDesc     string
	expiresOn     int
	homeAccountID string
	loginName     string
	token         string
}

type credential struct {
	authority     string
	clientID      string
	homeAccountID string
	opts          CredentialOptions
}

// NewCredential creates a new credential that uses OneAuth to broker authentication.
func NewCredential(authority, clientID string, opts CredentialOptions) (UserCredential, error) {
	if err := start(clientID, opts.Debug); err != nil {
		return nil, err
	}
	cred := &credential{
		authority:     authority,
		clientID:      clientID,
		homeAccountID: opts.HomeAccountID,
		opts:          opts,
	}
	return cred, nil
}

func (c *credential) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	at := azcore.AccessToken{}
	ar, err := authn(c.authority, c.clientID, c.homeAccountID, strings.Join(opts.Scopes, " "), c.opts.NoPrompt, c.opts.Debug)
	if err == nil {
		at.ExpiresOn = time.Unix(int64(ar.expiresOn), 0)
		at.Token = ar.token
		c.homeAccountID = ar.homeAccountID
	}
	return at, err
}

// HomeAccountID of the most recently authenticated user or an empty string, if no user has authenticated
func (c *credential) HomeAccountID() string {
	return c.homeAccountID
}

func Logout(clientID string, debug bool) error {
	err := start(clientID, debug)
	if err == nil {
		logout.Call()
	}
	return nil
}

func SignIn(authority, clientID, homeAccountID, scope string, debug bool) (string, error) {
	ar, err := authn(authority, clientID, homeAccountID, scope, false, debug)
	return ar.homeAccountID, err
}

func start(clientID string, debug bool) error {
	if started.CompareAndSwap(false, true) {
		err := loadDLL()
		if err != nil {
			return err
		}
		dbg := uintptr(0)
		if debug {
			dbg = uintptr(1)
		}
		clientID := unsafe.Pointer(C.CString(clientID))
		defer C.free(clientID)
		appID := unsafe.Pointer(C.CString(applicationID))
		defer C.free(appID)
		v := unsafe.Pointer(C.CString(internal.VersionInfo().Version.String()))
		defer C.free(v)
		msg, _, _ := startup.Call(uintptr(clientID), uintptr(appID), uintptr(v), dbg)
		// startup returns an error message when it fails
		if msg != 0 {
			// reset started so the next call will try to start OneAuth again
			started.CompareAndSwap(true, false)
			msg := C.GoString((*C.char)(unsafe.Pointer(msg)))
			return fmt.Errorf("couldn't start OneAuth: %s", msg)
		}
	}
	return nil
}

func authn(authority, clientID, homeAccountID, scope string, noPrompt, debug bool) (authResult, error) {
	res := authResult{}
	if err := start(clientID, debug); err != nil {
		return res, err
	}
	a := unsafe.Pointer(C.CString(authority))
	defer C.free(a)
	accountID := unsafe.Pointer(C.CString(homeAccountID))
	defer C.free(accountID)
	// OneAuth always appends /.default to scopes
	scope = strings.ReplaceAll(scope, "/.default", "")
	scp := unsafe.Pointer(C.CString(scope))
	defer C.free(scp)
	allowPrompt := 1
	if noPrompt {
		allowPrompt = 0
	}
	result, _, _ := authenticate.Call(uintptr(a), uintptr(accountID), uintptr(scp), uintptr(allowPrompt))
	if result == 0 {
		return res, fmt.Errorf("authentication failed")
	}
	defer freeAR.Call(result)

	ar := (*C.AuthnResult)(unsafe.Pointer(result))
	if ar.errorDescription != nil {
		res.errorDesc = C.GoString(ar.errorDescription)
		return res, fmt.Errorf(res.errorDesc)
	}

	res.expiresOn = int(ar.expiresOn)
	if ar.accountID != nil {
		res.homeAccountID = C.GoString(ar.accountID)
	}
	if ar.loginName != nil {
		res.loginName = C.GoString(ar.loginName)
	}
	if ar.token != nil {
		res.token = C.GoString(ar.token)
	}

	return res, nil
}

// loadDLL loads the bridge DLL and its dependencies, writing them to disk if necessary.
func loadDLL() error {
	if bridge != nil {
		return nil
	}
	// cacheDir is %LocalAppData%
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	for _, dll := range []struct {
		name           string
		checksum, data []byte
	}{
		{name: "fmt.dll", checksum: fmtChecksum, data: fmtDLL},
		{name: "bridge.dll", checksum: bridgeChecksum, data: bridgeDLL},
	} {
		hash, err := cmakeChecksumToBytes(dll.checksum)
		if err != nil {
			return fmt.Errorf("parsing checksum for %s: %w", dll.name, err)
		}
		err = writeDynamicLib(filepath.Join(cacheDir, "azd", dll.name), dll.data, hash)
		if err != nil {
			return err
		}
	}
	if err == nil {
		p := filepath.Join(cacheDir, "azd", "bridge.dll")
		bridge = windows.NewLazyDLL(p)
		authenticate = bridge.NewProc("Authenticate")
		freeAR = bridge.NewProc("FreeAuthnResult")
		logout = bridge.NewProc("Logout")
		shutdown = bridge.NewProc("Shutdown")
		startup = bridge.NewProc("Startup")
	}
	return err
}
