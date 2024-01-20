// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

#include "bridge.h"
#include <future>
#include <OneAuth/OneAuthWin.hpp>
#include <windows.h>

using namespace Microsoft::Authentication;
using Microsoft::Authentication::UUID;

const int timeoutSeconds = 60;

const char *Startup(const char *clientId, const char *applicationId, const char *version, bool debug)
{
    HRESULT OleInitResult = OleInitialize(NULL);
    if (OleInitResult != S_OK && OleInitResult != S_FALSE)
    {
        return "OleInitialize failed";
    }

    if (debug)
    {
        // TODO: is it okay for --debug to imply PII logging?
        OneAuth::SetLogPiiEnabled(true);
        OneAuth::SetLogLevel(LogLevel::LogLevelInfo);
    }
    else
    {
        OneAuth::SetLogLevel(LogLevel::LogLevelNoLog);
    }

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
// FreeAuthnResult to free memory allocated here.
AuthnResult *wrapAuthResult(const AuthResult *ar)
{
    auto wrapped = new AuthnResult();
    if (auto account = ar->GetAccount())
    {
        wrapped->accountID = strdup(account->GetHomeAccountId().c_str());
        wrapped->loginName = strdup(account->GetLoginName().c_str());
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

AuthnResult *Authenticate(const char *authority, const char *homeAccountID, const char *scope)
{
    auto authParams = AuthParameters::CreateForBearer(authority, scope);
    auto telemetryParams = TelemetryParameters(UUID::Generate());

    std::promise<AuthResult> promise;
    std::future<AuthResult> future = promise.get_future();
    auto callback = [&promise](const AuthResult &result)
    {
        // TODO: write to a Go channel?
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
        OneAuth::GetAuthenticator()->SignInInteractively(
            OneAuth::DefaultUxContext,
            "", // TODO: account hint?
            authParams,
            std::nullopt,
            telemetryParams,
            callback);

        // TODO: login window requires us to pump win32 messages
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
                auto ar = new AuthnResult();
                ar->errorDescription = strdup("timed out waiting for login");
                return ar;
            }
        }
    }

    auto res = future.get();
    if (auto account = res.GetAccount())
    {
        // TODO: have azd auth logout call DisassociateAccount
        // Note: don't call SignOutSilently because it deletes data based on client ID i.e. will sign out az accounts
        // (Dis/associate uses application ID instead e.g. "com.azure.azd")
        OneAuth::GetAuthenticator()->AssociateAccount(*account, telemetryParams);
    }
    return wrapAuthResult(&res);
}

void FreeAuthnResult(AuthnResult *authnResult)
{
    // free the C strings because they were allocated with strdup;
    // delete the struct because it was allocated with new
    free(authnResult->accountID);
    free(authnResult->errorDescription);
    free(authnResult->loginName);
    free(authnResult->token);
    delete authnResult;
}
