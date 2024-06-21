# External Authentication

## Problem

As part of its operation, `azd` needs to make calls to different Azure services. For example `azd provision` calls the ARM control plane to submit a deployment. `azd deploy` may need to make management or data plane calls to deploy the customer code that it has built.

When using the CLI directly, it is natural to use the authentication information for the current logged in user (managed by `azd auth login`) but `azd` can also be used on behalf of another tool.  For example, when using `azd` via the Visual Studio Code extension, it would be ideal if the operations used the same principal that the user is logged in with in the IDE.

The typical solution in the Azure SDKs is to have a credential type per authentication source (e.g we have a VisualStudioCodeCredential, a VisualStudioCredential) and have the tool select which credential type to use. In practice this has been fragile for a few reasons:

1. The implementation of these credentials is often complex and hard to maintain and breaks over time.
2. We need a new credential type per dev tool and the dev tools needs to communicate with `azd` which credential to use.

Instead of the above strategy, we'd like a way for `azd` to hand off authentication requests to an external process (i.e. the process that launched `azd` to complete some end to end operation). We would like this solution to be simple and be implementable by multiple hosts without any changes to `azd`.

## Solution

We have introduced a feature similar to managed identity - `azd` can proxy GetToken requests from it's `TokenCredential` interface to a remote service, which will fetch a token and then return it to `azd`.

When run, `azd` looks for two special environment variables:

- `AZD_AUTH_ENDPOINT`
- `AZD_AUTH_KEY`

When both are set, instead of using the built in authentication information, a special `TokenCredential` instance is constructed and used. The implementation of `GetToken` of this credential makes a POST call to a special endpoint:

`${AZD_AUTH_ENDPOINT}/token?api-version=2023-07-12-preview`

Setting the following headers:

- `Content-Type: application/json`
- `Authorization: Bearer ${AZD_AUTH_KEY}`

The use of `AZD_AUTH_KEY` allows the host to block requests coming from other clients on the same machine (since the it is expected the host runs a ephemeral HTTP server listing on `127.0.0.1` on a random port). It is expected that the host will generate a random string and use this as a shared key for the lifetime of an `azd` invocation.

The body of the request maps to the data passed to `GetToken` via the GetTokenOptions struct (we considered version 1.7.0 of the Azure SDK for Go core package):

```jsonc
{
    "scopes": [ "scope1" /*, "scope2", ... */ ],
    "tenantId": "<string>", // optional, used to override the default tenant.
}
```

The server should take this request and fetch a token using the given configuration and return it back to the client.  The shape of the response looks like one of the following:

### Success

```jsonc
{
  "status": "success",
  "token": "<string>", // the access token.
  "expiresOn": "<string>" // the expiration time of the token, expressed in RFC3339 format.
}
```

> Import note. The `expiresOn` field should be expressed in RFC3339 and using invariant culture. Using local machine time format can make azd to fail trying to parse the response.

### Failure

```jsonc
{
  "status": "error",
  "code": "string", // one of "GetTokenError" or "NotSignedInError"
  "message": "string" // a human readable error message.
}
```

`NotSignedInError` is the code that is returned when the auth server detects that the user is not signed in, and can be used by the client to provide a better error experience.  Other failures are returned as a `GetTokenError` and the message can match the error message returned by `GetToken` on the server.

The message is returned as is as the `error` for the `GetToken` call on the client side.

## Implementation

The `azd` CLI implements the client side of this feature in the [`pkg/auth/remote_credential.go`](../pkg/auth/remote_credential.go).

The VS Code implementation of the server is in [src/utils/authServer.ts](../../../ext/vscode/src/utils/).

## Open Issues

- [ ] As of `azcore@1.8.0`, there are now new additional properties on `TokenRequestOptions`: `EnableCAE` and `Claims` which are not yet supported by the external authentication flow. We need to support these properties in the external authentication flow (to do so we should bump the api-version and add the new parameters. They are both optional).

- [ ] Perhaps we should allow the host to respond to an OPTIONS request to `/token` to discover the API versions that the server supports, so we can call the latest version that the server supports, or fail if there server does not support some minimum version.

- [ ] How might we run this protocol over JSON-RPC 2.0 as we do in our `vs-server` instead of HTTP?