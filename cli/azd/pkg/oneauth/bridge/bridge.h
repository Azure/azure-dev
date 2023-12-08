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
        char *loginName;
        char *token;
    } AuthnResult;

    __declspec(dllexport) void FreeAuthnResult(AuthnResult *);

    // Startup OneAuth. Returns an error message if this fails, NULL if it succeeds.
    // The returned string must be freed by the caller. The parameters are:
    // - clientId: the client ID of the application
    // - applicationId: an identifier for the application e.g. "com.microsoft.azd"
    // - version: the application version
    // - debug: whether to enable OneAuth console logging
    __declspec(dllexport) const char *Startup(const char *clientId, const char *applicationId, const char *version, bool debug);

    // Authenticate acquires an access token, interactively signing in a user if necessary.
    __declspec(dllexport) AuthnResult *Authenticate(const char *authority, const char *homeAccountID, const char *scope);

    // Logout disassociates all accounts from the application. This prevents
    // Authenticate silently using them but doesn't delete any of their data.
    __declspec(dllexport) void Logout();

    __declspec(dllexport) void Shutdown();

#ifdef __cplusplus
}
#endif
