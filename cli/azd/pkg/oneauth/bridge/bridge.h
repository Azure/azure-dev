// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

#include <stdbool.h>

#ifdef __cplusplus
extern "C"
{
#endif

    typedef struct
    {
        char *accountID;
        char *errorDescription;
        int expiresOn;
        char *token;
    } WrappedAuthResult;

    __declspec(dllexport) void FreeWrappedAuthResult(WrappedAuthResult *);

    // Startup OneAuth. Returns an error message if this fails, NULL if it succeeds.
    // The parameters are:
    // - clientId: the client ID of the application
    // - applicationId: an identifier for the application e.g. "com.microsoft.azd"
    // - version: the application version
    // - debug: whether to enable OneAuth console logging, including PII
    __declspec(dllexport) char *Startup(const char *clientId, const char *applicationId, const char *version, bool debug);

    // Authenticate acquires an access token. It will display an interactive login window if necessary, unless allowPrompt is false.
    // The parameters are:
    // - authority: authority for token requests e.g. "https://login.microsoftonline.com/tenant"
    // - scope: scope of the desired access token
    // - homeAccountID: optional home account ID of a user to authenticate. Required for silent authentication. If no value
    //                  is given or no account associated with azd matches the given value, this function will fall back to
    //                  interactive authentication, provided allowPrompt is true.
    // - allowPrompt: whether to display an interactive login window when necessary
    __declspec(dllexport) WrappedAuthResult *Authenticate(const char *authority, const char *scope, const char *homeAccountID, bool allowPrompt);

    // Logout disassociates all accounts from the application. This prevents
    // Authenticate silently using them but doesn't delete any of their data.
    __declspec(dllexport) void Logout();

    __declspec(dllexport) void Shutdown();

#ifdef __cplusplus
}
#endif
