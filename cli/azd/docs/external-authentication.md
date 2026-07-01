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

## Transport selection via URL scheme

Starting with `azd` 1.26, `AZD_AUTH_ENDPOINT` accepts three URL schemes that
select the transport used to reach the host's token server. The HTTP request
body, response shape, and `api-version` are identical across schemes — only
how `azd` dials the server changes.

### `https:` (existing)

Loopback HTTPS server.

- URL form: `https://host:port` (host is typically `127.0.0.1`).
- `AZD_AUTH_CERT` is **required** and must be a base64-encoded DER X.509
  certificate that the host's HTTPS server presents. `azd` pins the
  connection to this certificate.
- `AZD_AUTH_KEY` is **required** and is sent as `Authorization: Bearer <key>`.
- `azd` rejects the `https:` scheme when no cert is provided.

### `unix:` (new, POSIX only)

Unix domain socket transport. The OS enforces caller identity via filesystem
permissions, so no TLS handshake and no shared bearer secret are required.

- URL form: `unix:/absolute/path/to/socket` or
  `unix:///absolute/path/to/socket`. The socket path is taken from the URL's
  path component. Relative paths are an error.
- `AZD_AUTH_CERT` **MUST NOT** be set. If set, `azd` fails fast with a clear
  error.
- `AZD_AUTH_KEY` is **required** and is sent as `Authorization: Bearer <key>`.
- The socket path **MUST NOT** be a symlink. `azd` rejects symlinked socket
  paths outright so a link into a less-restricted directory cannot bypass the
  parent-directory permission check.
- **IDE host requirements:** the socket file MUST be created with mode `0600`
  and the parent directory MUST be mode `0700`, both owned by the current
  uid. `azd` `stat()`s the socket and the parent directory on first connect
  and refuses if either is group- or world-accessible, or if the owner
  differs from the `azd` process's effective uid. The connection fails with
  a clear "permissions too permissive" error.
- The HTTP request line still targets `/token?api-version=2023-07-12-preview`;
  the URL host is irrelevant and `azd` rewrites the request URL to
  `http://azd-auth/token?...` before dispatch.
- Path length: callers should be aware of OS limits (108 bytes on Linux, 104
  on macOS including the null terminator).

### `npipe:` (new, Windows only)

Windows named pipe transport. The OS enforces caller identity via the pipe's
security descriptor, so no TLS handshake and no shared bearer secret are
required.

- URL form: `npipe:azd-auth-<arbitrary>` (the value after `npipe:` is the
  pipe name; `azd` prepends `\\.\pipe\` automatically) **or**
  `npipe:////./pipe/azd-auth-<arbitrary>` (fully qualified). Both forms are
  accepted.
- `AZD_AUTH_CERT` **MUST NOT** be set. Same handling as `unix:`.
- `AZD_AUTH_KEY` is **required**. Same handling as `unix:`.
- **IDE host requirements:** the pipe MUST be created with a security
  descriptor that grants access only to the current user SID (and SYSTEM /
  Administrators, as is conventional). `azd` queries the pipe's DACL after
  connecting and refuses if any other SID has an allow ACE. The connection
  fails with a clear "permissions too permissive" error.
- Same `/token?api-version=...` and URL-rewrite behavior as `unix:`.

### Backward compatibility

An `AZD_AUTH_ENDPOINT` without a scheme, or with `https:`, behaves exactly as
it always has. No existing IDE host configuration is broken by the addition
of `unix:` and `npipe:`.

## Implementation

The `azd` CLI implements the client side of this feature in the [`pkg/auth/remote_credential.go`](../pkg/auth/remote_credential.go).

The VS Code implementation of the server is in [src/utils/authServer.ts](../../../ext/vscode/src/utils/).

## Open Issues

- [ ] As of `azcore@1.8.0`, there are now new additional properties on `TokenRequestOptions`: `EnableCAE` and `Claims` which are not yet supported by the external authentication flow. We need to support these properties in the external authentication flow (to do so we should bump the api-version and add the new parameters. They are both optional).

- [ ] Perhaps we should allow the host to respond to an OPTIONS request to `/token` to discover the API versions that the server supports, so we can call the latest version that the server supports, or fail if there server does not support some minimum version.

- [ ] How might we run this protocol over JSON-RPC 2.0 as we do in our `vs-server` instead of HTTP?