// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

#include <stdbool.h>

#ifdef __cplusplus
extern "C"
{
#endif

    typedef void (*Logger)(const char *);

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

    __declspec(dllexport) void FreeWrappedAuthResult(WrappedAuthResult *);
    __declspec(dllexport) void FreeWrappedError(WrappedError *);

    // Startup OneAuth. Returns an error message if this fails, NULL if it succeeds.
    // The parameters are:
    // - clientId: the client ID of the application
    // - applicationId: an identifier for the application e.g. "com.microsoft.azd"
    // - version: the application version
    // - logCallback: a function to call with log messages
    __declspec(dllexport) WrappedError *Startup(const char *clientId, const char *applicationId, const char *version, Logger logCallback);

    // Authenticate acquires an access token. It will display an interactive login window if necessary, unless allowPrompt is false.
    // The parameters are:
    // - authority: authority for token requests e.g. "https://login.microsoftonline.com/tenant"
    // - scope: scope of the desired access token
    // - accountID: optional account ID of a user to authenticate, as returned by a previous call to this function. Required for silent
    //              authentication. If empty or no account associated with azd matches the given value, this function will fall back to
    //              interactive authentication, provided allowPrompt is true.
    // - allowPrompt: whether to display an interactive login window when necessary
    __declspec(dllexport) WrappedAuthResult *Authenticate(const char *authority, const char *scope, const char *accountID, bool allowPrompt);

    // SignInSilently authenticates an account inferred from the OS e.g. the active Windows user, without displaying UI.
    // It returns an error when that's impossible.
    __declspec(dllexport) WrappedAuthResult *SignInSilently();

    // Logout disassociates all accounts from the application.
    __declspec(dllexport) void Logout();

    __declspec(dllexport) void Shutdown();

#ifdef __cplusplus
}
#endif
