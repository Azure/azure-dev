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

	// Supported indicates whether brokered authentication is supported.
	Supported = true
)

var (
	//go:embed bridge/_build/Release/bridge.dll
	bridgeDLL []byte
	//go:embed bridge/_build/Release/bridge.dll.sha256
	bridgeHash []byte

	//go:embed bridge/_build/Release/fmt.dll
	fmtDLL []byte
	//go:embed bridge/_build/Release/fmt.dll.sha256
	fmtHash []byte

	// bridge is a lazy-loaded DLL that provides access to the OneAuth API
	bridge       *windows.LazyDLL
	authenticate *windows.LazyProc
	freeAR       *windows.LazyProc
	shutdown     *windows.LazyProc
	startup      *windows.LazyProc

	// started tracks whether the bridge's Startup function has been called because OneAuth
	// returns an error when its Startup function is called more than once
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

	// TODO: get this from internal.GlobalCommandOptions
	debug bool
}

// NewCredential creates a new credential that uses OneAuth to broker authentication.
// If homeAccountID is empty, GetToken will prompt a user to authenticate.
func NewCredential(authority, clientID, homeAccountID string) (UserCredential, error) {
	if err := findDLLs(); err != nil {
		return nil, err
	}
	cred := &credential{
		clientID:      clientID,
		homeAccountID: homeAccountID,
	}
	return cred, nil
}

func (c *credential) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	at := azcore.AccessToken{}

	if started.CompareAndSwap(false, true) {
		dbg := uintptr(0)
		if c.debug {
			dbg = uintptr(1)
		}
		clientID := unsafe.Pointer(C.CString(c.clientID))
		defer C.free(clientID)
		appID := unsafe.Pointer(C.CString(applicationID))
		defer C.free(appID)
		v := unsafe.Pointer(C.CString(internal.VersionInfo().Version.String()))
		defer C.free(v)
		msg, _, _ := startup.Call(uintptr(clientID), uintptr(appID), uintptr(v), dbg)
		if msg != 0 {
			msg := C.GoString((*C.char)(unsafe.Pointer(msg)))
			return at, fmt.Errorf("couldn't start OneAuth: %s", msg)
		}
	}

	// OneAuth always appends /.default to scopes
	scopes := make([]string, len(opts.Scopes))
	for i, scope := range opts.Scopes {
		scopes[i] = strings.TrimSuffix(scope, "/.default")
	}
	// TODO: claims
	ar, err := c.Authenticate(c.homeAccountID, strings.Join(scopes, " "))
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

func (c *credential) Authenticate(homeAccountID, scope string) (authResult, error) {
	res := authResult{}
	if oid, _, found := strings.Cut(homeAccountID, "."); found {
		homeAccountID = oid
	}
	authority := unsafe.Pointer(C.CString(c.authority))
	defer C.free(authority)
	accountID := unsafe.Pointer(C.CString(homeAccountID))
	defer C.free(accountID)
	scp := unsafe.Pointer(C.CString(scope))
	defer C.free(scp)
	result, _, _ := authenticate.Call(uintptr(authority), uintptr(accountID), uintptr(scp))
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

// TODO: check hash
func findDLLs() error {
	if bridge != nil {
		return nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	write := func(name string, data []byte) error {
		p := filepath.Join(filepath.Dir(exePath), name)
		if _, err := os.Stat(p); err == nil {
			err = os.Remove(p)
			if err != nil {
				return fmt.Errorf("couldn't remove %s: %w", p, err)
			}
		}
		err = os.WriteFile(p, data, 0600)
		return err
	}
	err = write("fmt.dll", fmtDLL)
	if err == nil {
		err = write("bridge.dll", bridgeDLL)
	}
	if err == nil {
		p := filepath.Join(filepath.Dir(exePath), "bridge.dll")
		bridge = windows.NewLazyDLL(p)
		authenticate = bridge.NewProc("Authenticate")
		freeAR = bridge.NewProc("FreeAuthnResult")
		shutdown = bridge.NewProc("Shutdown")
		startup = bridge.NewProc("Startup")
	}
	return err
}
