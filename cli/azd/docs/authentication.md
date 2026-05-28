# Authentication

This document covers the authentication methods supported by `azd auth login` and how `azd` manages
authentication state.

## Authentication methods

### Interactive browser login (default)

The default method. Running `azd auth login` opens a browser window for you to sign in with your
Microsoft Entra ID (Azure AD) account.

```bash
azd auth login
```

To target a specific tenant:

```bash
azd auth login --tenant-id <tenant-id-or-domain>
```

To choose the local port used for the redirect URI during the browser flow:

```bash
azd auth login --redirect-port 8080
```

### Device code login

Use device code flow when a browser is not available on the current machine (e.g. SSH sessions,
containers, Codespaces in a browser).

```bash
azd auth login --use-device-code
```

### Service principal with client secret

Authenticate as a service principal using a client secret. Both `--client-id` and `--tenant-id` are
required.

```bash
azd auth login \
  --client-id <app-id> \
  --tenant-id <tenant-id> \
  --client-secret <secret>
```

If you pass `--client-secret` with an empty value, `azd` prompts you to enter the secret
interactively (useful to avoid leaking secrets in shell history).

### Service principal with client certificate

Authenticate using a PEM-encoded certificate file.

```bash
azd auth login \
  --client-id <app-id> \
  --tenant-id <tenant-id> \
  --client-certificate /path/to/cert.pem
```

### Federated credentials (OIDC)

Federated token providers allow authentication without secrets in CI/CD environments using
OpenID Connect (OIDC).

#### GitHub Actions

```bash
azd auth login \
  --client-id <app-id> \
  --tenant-id <tenant-id> \
  --federated-credential-provider github
```

The `ACTIONS_ID_TOKEN_REQUEST_URL` and `ACTIONS_ID_TOKEN_REQUEST_TOKEN` environment variables must be
available (GitHub sets these automatically when `id-token: write` is granted in the workflow).

#### Azure Pipelines

```bash
azd auth login \
  --client-id <app-id> \
  --tenant-id <tenant-id> \
  --federated-credential-provider azure-pipelines
```

When using `azure-pipelines`, the following environment variables are read automatically if
`--client-id` or `--tenant-id` are not provided:

| Variable | Description |
|---|---|
| `AZURESUBSCRIPTION_CLIENT_ID` | Client ID of the service connection |
| `AZURESUBSCRIPTION_TENANT_ID` | Tenant ID of the service connection |
| `AZURESUBSCRIPTION_SERVICE_CONNECTION_ID` | Service connection ID (required) |
| `SYSTEM_ACCESSTOKEN` | Pipeline system access token (must be mapped via `env`) |

#### Generic OIDC

For other OIDC-compatible providers:

```bash
azd auth login \
  --client-id <app-id> \
  --tenant-id <tenant-id> \
  --federated-credential-provider oidc
```

### Managed identity

Authenticate using a managed identity when running on an Azure compute resource (VMs, App Service,
Container Apps, etc.).

```bash
# System-assigned managed identity
azd auth login --managed-identity

# User-assigned managed identity
azd auth login --managed-identity --client-id <managed-identity-client-id>
```

### Delegated authentication (Azure CLI)

You can configure `azd` to delegate authentication to the Azure CLI (`az`) instead of managing
credentials itself. This is useful when `azd` does not yet support your preferred authentication
method.

```bash
azd config set auth.useAzCliAuth true
```

When this is set, `azd auth login` detects the delegated mode and offers to switch back to
built-in authentication. To authenticate, use `az login` directly.

### External authentication

When `azd` is launched by a host tool (e.g. the VS Code extension), the host can provide
authentication by setting the `AZD_AUTH_ENDPOINT` and `AZD_AUTH_KEY` environment variables. In this
mode, `azd` proxies all token requests to the host process.

For full details on the external authentication protocol, see
[External Authentication](external-authentication.md).

## Checking login status

To verify whether you are currently logged in without triggering a new login flow:

```bash
azd auth login --check-status
```

This prints the current authentication status and exits. Use `--output json` for machine-readable
output that includes the token expiration time.

## Logging out

To sign out and remove cached authentication data:

```bash
azd auth logout
```

This removes the current user from the MSAL cache, deletes stored service principal credentials,
and clears the subscriptions cache.

## Resetting authentication state (`--reset`)

> **Added in:** [#7541](https://github.com/Azure/azure-dev/issues/7541)

In rare cases, `azd` may report an expired or invalid token error (e.g. `AADSTS700082: The refresh
token has expired due to inactivity`) even immediately after a successful `azd auth login`. This
happens because stale data in the local MSAL token cache or credential files can interfere with
the new login session.

The `--reset` flag performs a complete cleanup of all locally cached authentication data **before**
logging in, giving you a clean slate:

```bash
azd auth login --reset
```

### What `--reset` clears

| Item | Path | Description |
|---|---|---|
| MSAL token cache | `~/.azd/auth/msal/` | Cached access and refresh tokens from MSAL |
| Credential cache | `~/.azd/auth/` | Stored service principal secrets and certificates |
| Auth config | `~/.azd/auth.json` | Current user identity metadata |
| Claims file | `~/.azd/auth.claims` | Cached claims from previous login |
| Subscription cache | `~/.azd/subscriptions.cache` | Cached list of accessible subscriptions |

After clearing, the directory structure is recreated and the normal login flow proceeds. This is
equivalent to manually deleting these files and then running `azd auth login`.

### When to use `--reset`

Use `--reset` when:

- You see `AADSTS700082` or similar stale-token errors right after logging in successfully
- `azd` commands fail with authentication errors that persist across multiple `azd auth login`
  attempts
- You want to ensure a completely fresh authentication state (e.g. after switching tenants
  or accounts)

The flag can be combined with any other login method:

```bash
# Reset and log in interactively
azd auth login --reset

# Reset and log in with device code
azd auth login --reset --use-device-code

# Reset and log in as a service principal
azd auth login --reset --client-id <app-id> --tenant-id <tenant-id> --client-secret <secret>
```

## How authentication state is stored

`azd` stores authentication data under the user configuration directory (default `~/.azd/`,
overridable via `AZD_CONFIG_DIR`):

```text
~/.azd/
├── auth.json                 # Current user identity (home account ID, client/tenant IDs)
├── auth.claims               # Cached claims for re-login
├── subscriptions.cache       # Cached Azure subscriptions
└── auth/
    └── msal/
        ├── cache*.json       # MSAL token cache (access/refresh tokens)
        └── cred*.json        # Service principal credential cache
```

On Windows, the MSAL cache is encrypted using `CryptProtectData` and stored as `.bin` files instead
of `.json`. On all platforms, auth files are ACL'd to be readable only by the current user.
