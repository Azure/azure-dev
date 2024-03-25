// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

//go:build oneauth && windows

package oneauth

/*
#include <stdbool.h>
#include <stdlib.h>

// forward declaration; definition in c_funcs.go
void goLogGateway(char *s);

// Below definitions must match the ones in bridge.h exactly. We don't include
// bridge.h because doing so would make the bridge DLL a dependency of azd.exe
// and prevent distributing the DLL via embedding because Windows won't execute
// a program's entry point if its DLL dependencies are unavailable.

typedef void (*Logger)(char *);

typedef struct
{
	char *accountID;
	char *errorDescription;
	int expiresOn;
	char *token;
} WrappedAuthResult;

typedef struct
{
	char *message;
} WrappedError;
*/
import "C"

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/azure/azure-dev/cli/azd/internal"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"golang.org/x/sys/windows"
)

//export goLog
func goLog(s *C.char) {
	log.Print(C.GoString(s))
}

// Supported indicates whether this build includes OneAuth integration.
const Supported = true

var (
	//go:embed bridge/_build/Release/bridge.dll
	bridgeDLL []byte
	//go:embed bridge/_build/Release/bridge.dll.sha256
	bridgeChecksum string
	//go:embed bridge/_build/Release/fmt.dll
	fmtDLL []byte
	//go:embed bridge/_build/Release/fmt.dll.sha256
	fmtChecksum string

	// bridge provides access to the OneAuth API
	bridge         *windows.DLL
	authenticate   *windows.Proc
	freeAR         *windows.Proc
	freeError      *windows.Proc
	logout         *windows.Proc
	shutdown       *windows.Proc
	signInSilently *windows.Proc
	startup        *windows.Proc
)

func Shutdown() {
	if started.CompareAndSwap(true, false) {
		shutdown.Call()
	}
}

type authResult struct {
	homeAccountID string
	token         azcore.AccessToken
}

type credential struct {
	authority     string
	clientID      string
	homeAccountID string
	opts          CredentialOptions
}

// NewCredential creates a new credential that acquires tokens via OneAuth.
func NewCredential(authority, clientID string, opts CredentialOptions) (azcore.TokenCredential, error) {
	cred := &credential{
		authority:     authority,
		clientID:      clientID,
		homeAccountID: opts.HomeAccountID,
		opts:          opts,
	}
	return cred, nil
}

// GetToken acquires a token from OneAuth. If doing so requires user interaction and NoPrompt is true, it returns
// an error. Otherwise, OneAuth will display a login window and this call must occur on the main thread.
func (c *credential) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	ar, err := authn(c.authority, c.clientID, c.homeAccountID, strings.Join(opts.Scopes, " "), c.opts.NoPrompt)
	if err == nil {
		c.homeAccountID = ar.homeAccountID
	}
	return ar.token, err
}

func LogIn(authority, clientID, scope string) (string, error) {
	ar, err := authn(authority, clientID, "", scope, false)
	return ar.homeAccountID, err
}

func Logout(clientID string) error {
	err := start(clientID)
	if err == nil {
		logout.Call()
	}
	return err
}

// LogInSilently attempts to log in the active Windows user and return that user's account ID. It never displays UI.
func LogInSilently(clientID string) (string, error) {
	err := start(clientID)
	if err != nil {
		return "", err
	}
	p, _, _ := signInSilently.Call()
	if p == 0 {
		return "", fmt.Errorf("silent login failed")
	}
	defer freeAR.Call(p)
	wrapped := (*C.WrappedAuthResult)(unsafe.Pointer(p))
	if wrapped.errorDescription != nil {
		return "", fmt.Errorf(C.GoString(wrapped.errorDescription))
	}
	accountID := C.GoString(wrapped.accountID)
	return accountID, err
}

func start(clientID string) error {
	if started.CompareAndSwap(false, true) {
		err := loadDLL()
		if err != nil {
			return err
		}
		clientID := unsafe.Pointer(C.CString(clientID))
		defer C.free(clientID)
		appID := unsafe.Pointer(C.CString(applicationID))
		defer C.free(appID)
		v := unsafe.Pointer(C.CString(internal.VersionInfo().Version.String()))
		defer C.free(v)
		p, _, _ := startup.Call(
			uintptr(clientID),
			uintptr(appID),
			uintptr(v),
			uintptr(unsafe.Pointer(C.goLogGateway)),
		)
		// startup returns a char* message when it fails
		if p != 0 {
			// reset started so the next call will try to start OneAuth again
			started.CompareAndSwap(true, false)
			defer freeError.Call(p)
			wrapped := (*C.WrappedError)(unsafe.Pointer(p))
			return fmt.Errorf("couldn't start OneAuth: %s", C.GoString(wrapped.message))
		}
	}
	return nil
}

func authn(authority, clientID, homeAccountID, scope string, noPrompt bool) (authResult, error) {
	res := authResult{}
	if err := start(clientID); err != nil {
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
	p, _, _ := authenticate.Call(uintptr(a), uintptr(scp), uintptr(accountID), uintptr(allowPrompt))
	if p == 0 {
		// this shouldn't happen but if it did, this vague error would be better than a panic
		return res, fmt.Errorf("authentication failed")
	}
	defer freeAR.Call(p)

	wrapped := (*C.WrappedAuthResult)(unsafe.Pointer(p))
	if wrapped.errorDescription != nil {
		return res, fmt.Errorf(C.GoString(wrapped.errorDescription))
	}
	if wrapped.accountID != nil {
		res.homeAccountID = C.GoString(wrapped.accountID)
	}
	if wrapped.token != nil {
		res.token = azcore.AccessToken{
			ExpiresOn: time.Unix(int64(wrapped.expiresOn), 0),
			Token:     C.GoString(wrapped.token),
		}
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
	dir := filepath.Join(cacheDir, "azd")
	for _, dll := range []struct {
		name, checksum string
		data           []byte
	}{
		{name: "fmt.dll", checksum: fmtChecksum, data: fmtDLL},
		{name: "bridge.dll", checksum: bridgeChecksum, data: bridgeDLL},
	} {
		p := filepath.Join(dir, dll.name)
		err = writeDynamicLib(p, dll.data, dll.checksum)
		if err != nil {
			return fmt.Errorf("writing %s: %w", p, err)
		}
	}
	p := filepath.Join(dir, "bridge.dll")
	h, err := windows.LoadLibraryEx(p, 0, windows.LOAD_LIBRARY_SEARCH_DEFAULT_DIRS|windows.LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR)
	if err == nil {
		bridge = &windows.DLL{Handle: h, Name: p}
		authenticate, err = bridge.FindProc("Authenticate")
	}
	if err == nil {
		freeAR, err = bridge.FindProc("FreeWrappedAuthResult")
	}
	if err == nil {
		freeError, err = bridge.FindProc("FreeWrappedError")
	}
	if err == nil {
		logout, err = bridge.FindProc("Logout")
	}
	if err == nil {
		shutdown, err = bridge.FindProc("Shutdown")
	}
	if err == nil {
		signInSilently, err = bridge.FindProc("SignInSilently")
	}
	if err == nil {
		startup, err = bridge.FindProc("Startup")
	}
	return err
}
