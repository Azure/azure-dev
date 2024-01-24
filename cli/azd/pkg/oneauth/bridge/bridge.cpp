// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

#include "bridge.h"
#include <future>
#include <OneAuth/OneAuthWin.hpp>
#include <windows.h>

using namespace Microsoft::Authentication;
using Microsoft::Authentication::UUID;

const int timeoutSeconds = 60;

static std::function<void(const char *)> globalLogCallback;
void logCallback(LogLevel level, const char *message, int identifiableInformation)
{
    if (!identifiableInformation && globalLogCallback)
    {
        globalLogCallback(message);
    }
}

char *Startup(const char *clientId, const char *applicationId, const char *version, Logger logger)
{
    HRESULT OleInitResult = OleInitialize(NULL);
    if (OleInitResult != S_OK && OleInitResult != S_FALSE)
    {
        return "OleInitialize failed";
    }

    globalLogCallback = logger;
    OneAuth::SetLogCallback(logCallback);
    OneAuth::SetLogLevel(LogLevel::LogLevelInfo);

    auto appConfig = AppConfiguration(applicationId, "azd", version, "en");

    // Default resource/scope is irrelevant because azd always specifies the scope, however
    // OneAuth doesn't accept "". Also, OneAuth appends "/.default" to scopes.
    auto aadConfig = std::make_optional<AadConfiguration>(
        UUID::FromString(clientId),
        "http://localhost",               // redirectUri
        "https://management.azure.com/"); // defaultSignInResource

    auto msaConfig = std::make_optional<MsaConfiguration>(
        clientId,
        "http://localhost",               // redirectUri
        "https://management.azure.com/"); // defaultSignInScope

    auto authnConfig = AuthenticatorConfiguration(appConfig, aadConfig, msaConfig, std::nullopt, std::nullopt);
    if (auto error = OneAuth::Startup(authnConfig))
    {
        auto copy = strdup(error->ToString().c_str());
        return copy;
    }

    return nullptr;
}

void Shutdown()
{
    OneAuth::Shutdown();
    OleUninitialize();
}

// wrapAuthResult copies the data from a OneAuth AuthResult into a new struct that can be returned to
// Go. An AuthResult itself can't be returned to Go because it contains shared_ptrs that may be freed
// before Go is done with them. This workaround makes the Go application responsible for calling
// FreeWrappedAuthResult to free memory allocated here.
WrappedAuthResult *wrapAuthResult(const AuthResult *ar)
{
    auto wrapped = new WrappedAuthResult();
    if (auto account = ar->GetAccount())
    {
        wrapped->accountID = strdup(account->GetHomeAccountId().c_str());
    }
    if (auto credential = ar->GetCredential())
    {
        auto duration = credential->GetExpiresOn().time_since_epoch();
        wrapped->expiresOn = std::chrono::duration_cast<std::chrono::seconds>(duration).count();
        wrapped->token = strdup(credential->GetValue().c_str());
    }
    if (auto error = ar->GetError())
    {
        auto err = error->ToString();
        wrapped->errorDescription = strdup(err.c_str());
    }
    return wrapped;
}

WrappedAuthResult *Authenticate(const char *authority, const char *scope, const char *homeAccountID, bool allowPrompt)
{
    auto authParams = AuthParameters::CreateForBearer(authority, scope);
    auto telemetryParams = TelemetryParameters(UUID::Generate());

    std::promise<AuthResult> promise;
    std::future<AuthResult> future = promise.get_future();
    auto callback = [&promise](const AuthResult &result)
    {
        promise.set_value(result);
    };

    std::shared_ptr<Account> account = nullptr;
    if (strlen(homeAccountID) > 0)
    {
        for (auto a : OneAuth::GetAuthenticator()->ReadAssociatedAccounts(telemetryParams))
        {
            if (a.GetHomeAccountId() == homeAccountID)
            {
                account = std::make_shared<Account>(a);
                break;
            }
        }
    }

    if (account)
    {
        OneAuth::GetAuthenticator()->AcquireCredentialSilently(*account, authParams, telemetryParams, callback);
        // impose a deadline because we don't want to hang should OneAuth not call the callback
        future.wait_for(std::chrono::seconds(timeoutSeconds));
    }

    // if the future isn't ready, we didn't find an account or silent auth failed or timed out
    // (we don't care why silent auth failed because in any case we would fall back to interactive auth)
    if (future.wait_for(std::chrono::seconds(0)) != std::future_status::ready)
    {
        if (!allowPrompt)
        {
            auto ar = new WrappedAuthResult();
            ar->errorDescription = strdup("Interactive authentication is required. Run 'azd auth login'");
            return ar;
        }

        OneAuth::GetAuthenticator()->SignInInteractively(
            OneAuth::DefaultUxContext,
            "", // accountHint
            authParams,
            std::nullopt,
            telemetryParams,
            callback);

        // login window requires us to pump win32 messages
        auto start = std::chrono::steady_clock::now();
        MSG msg;
        while (GetMessage(&msg, nullptr, 0, 0))
        {
            TranslateMessage(&msg);
            DispatchMessage(&msg);

            if (future.wait_for(std::chrono::seconds(0)) == std::future_status::ready)
            {
                PostQuitMessage(0);
            }
            else if (std::chrono::steady_clock::now() - start >= std::chrono::seconds(timeoutSeconds))
            {
                PostQuitMessage(0);
                auto ar = new WrappedAuthResult();
                ar->errorDescription = strdup("timed out waiting for login");
                return ar;
            }
        }
    }

    auto res = future.get();
    if (auto account = res.GetAccount())
    {
        OneAuth::GetAuthenticator()->AssociateAccount(*account, telemetryParams);
    }
    return wrapAuthResult(&res);
}

void Logout()
{
    auto telemetryParams = TelemetryParameters(UUID::Generate());
    for (auto a : OneAuth::GetAuthenticator()->ReadAssociatedAccounts(telemetryParams))
    {
        // SignOut* delete data based on client ID i.e. they would sign the account
        // out from az as well so long as azd and az share a client ID. Dis/associate
        // use application ID e.g. "com.microsoft.azd" instead.
        OneAuth::GetAuthenticator()->DisassociateAccount(a, telemetryParams, "");
    }
}

void FreeWrappedAuthResult(WrappedAuthResult *WrappedAuthResult)
{
    // free the C strings because they were allocated with strdup;
    // delete the struct because it was allocated with new
    free(WrappedAuthResult->accountID);
    free(WrappedAuthResult->errorDescription);
    free(WrappedAuthResult->token);
    delete WrappedAuthResult;
}
