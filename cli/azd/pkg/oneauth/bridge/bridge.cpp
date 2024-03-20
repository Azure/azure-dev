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

WrappedError *Startup(const char *clientId, const char *applicationId, const char *version, Logger logger)
{
    HRESULT OleInitResult = OleInitialize(NULL);
    if (OleInitResult != S_OK && OleInitResult != S_FALSE)
    {
        auto err = new WrappedError();
        err->message = strdup("OleInitialize failed");
        return err;
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

    auto authnConfig = AuthenticatorConfiguration(appConfig, aadConfig, std::nullopt, std::nullopt, std::nullopt);
    if (auto error = OneAuth::Startup(authnConfig))
    {
        auto wrapped = new WrappedError();
        wrapped->message = strdup(error->ToString().c_str());
        return wrapped;
    }

    return nullptr;
}

void Shutdown()
{
    OneAuth::Shutdown();
    OleUninitialize();
}

WrappedAccounts *ListAccounts()
{
    auto result = new WrappedAccounts();
    result->accounts = nullptr;
    result->count = 0;
    result->err = nullptr;
    auto promise = std::promise<void>();
    auto telemetryParams = TelemetryParameters(UUID::Generate());
    OneAuth::GetAuthenticator()->DiscoverAccounts(std::nullopt, telemetryParams, [&promise](const DiscoveryResult &result)
                                                  {
        if (result.IsCompleted())
        {
            promise.set_value();
            return true;
        }
        return false; });
    auto future = promise.get_future();
    if (future.wait_for(std::chrono::seconds(timeoutSeconds)) != std::future_status::ready)
    {
        result->err = new WrappedError();
        result->err->message = strdup("timed out waiting for account discovery");
        return result;
    }

    auto accounts = OneAuth::GetAuthenticator()->ReadAllAccounts(telemetryParams);
    result->count = accounts.size();
    result->accounts = new WrappedAccount[accounts.size()];
    for (size_t i = 0; i < accounts.size(); i++)
    {
        result->accounts[i].id = strdup(accounts[i].GetId().c_str());
        result->accounts[i].username = strdup(accounts[i].GetLoginName().c_str());
        result->accounts[i].displayName = strdup(accounts[i].GetDisplayName().c_str());

        result->accounts[i].associations = nullptr;
        std::vector<std::string> associatedWith;
        auto associations = accounts[i].GetAssociationStatus();
        for (auto &a : associations)
        {
            if (a.second == AssociationStatus::Associated)
            {
                associatedWith.push_back(a.first);
            }
        }
        result->accounts[i].associationCount = associatedWith.size();
        if (auto size = associatedWith.size())
        {
            result->accounts[i].associationCount = size;
            result->accounts[i].associations = new char *[size];
            for (size_t j = 0; j < size; j++)
            {
                result->accounts[i].associations[j] = strdup(associatedWith[j].c_str());
            }
        }
    }
    return result;
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
        wrapped->accountID = strdup(account->GetId().c_str());
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

WrappedAuthResult *Authenticate(const char *authority, const char *scope, const char *accountID, bool allowPrompt)
{
    auto authParams = AuthParameters::CreateForBearer(authority, scope);
    auto telemetryParams = TelemetryParameters(UUID::Generate());

    std::promise<AuthResult> promise;
    std::future<AuthResult> future = promise.get_future();
    auto callback = [&promise](const AuthResult &result)
    {
        promise.set_value(result);
    };

    if (accountID && strlen(accountID) > 0)
    {
        if (auto account = OneAuth::GetAuthenticator()->ReadAccountById(accountID, telemetryParams))
        {
            OneAuth::GetAuthenticator()->AcquireCredentialSilently(*account, authParams, telemetryParams, callback);
            // impose a deadline because we don't want to hang should OneAuth not call the callback
            future.wait_for(std::chrono::seconds(timeoutSeconds));
        }
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

        // Login window requires us to pump win32 messages. Check the future before starting the pump because
        // SignInInteractively may call back with an error before displaying the login window, in which case
        // GetMessage will never return because there will never be a message in the queue, because azd has no
        // windows.
        MSG msg;
        auto ready = future.wait_for(std::chrono::seconds(0)) == std::future_status::ready;
        auto start = std::chrono::steady_clock::now();
        auto timedOut = false;
        while (!(ready || timedOut))
        {
            GetMessage(&msg, nullptr, 0, 0);
            TranslateMessage(&msg);
            DispatchMessage(&msg);
            ready = future.wait_for(std::chrono::seconds(0)) == std::future_status::ready;
            timedOut = std::chrono::steady_clock::now() - start >= std::chrono::seconds(timeoutSeconds);
            if (ready || timedOut)
            {
                PostQuitMessage(0);
            }
        }
        if (timedOut)
        {
            auto ar = new WrappedAuthResult();
            ar->errorDescription = strdup("timed out waiting for login");
            return ar;
        }
    }

    auto res = future.get();
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

void FreeWrappedAccounts(WrappedAccounts *accounts)
{
    if (accounts)
    {
        for (int i = 0; i < accounts->count; i++)
        {
            free(accounts->accounts[i].id);
            free(accounts->accounts[i].username);
            free(accounts->accounts[i].displayName);
            if (accounts->accounts[i].associations)
            {
                for (int j = 0; j < accounts->accounts[i].associationCount; j++)
                {
                    free(accounts->accounts[i].associations[j]);
                }
                delete[] accounts->accounts[i].associations;
            }
        }
        delete[] accounts->accounts;
        delete accounts;
    }
}

void FreeWrappedAuthResult(WrappedAuthResult *WrappedAuthResult)
{
    // free the C strings because they were allocated with strdup;
    // delete the struct because it was allocated with new
    if (WrappedAuthResult)
    {
        free(WrappedAuthResult->accountID);
        free(WrappedAuthResult->errorDescription);
        free(WrappedAuthResult->token);
        delete WrappedAuthResult;
    }
}

void FreeWrappedError(WrappedError *error)
{
    if (error)
    {
        free(error->message);
        delete error;
    }
}
