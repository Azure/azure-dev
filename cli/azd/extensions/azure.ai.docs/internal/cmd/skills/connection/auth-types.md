---
short: Reference of auth types, credential shapes, and PARAM_* externalization.
order: 40
---
# Connection auth types

The `authType:` field picks what kind of credential the Foundry runtime injects at tool-call time. The `credentials:` map's shape depends entirely on this value.

For scenario examples, see `add`. For category selection, see `categories`.

## Auth-type table

| `authType:`                  | CLI flag                       | Credentials                                                                | Common with                                       |
| ---------------------------- | ------------------------------ | -------------------------------------------------------------------------- | ------------------------------------------------- |
| `ApiKey`                     | `api-key`                      | `{ key: <string> }`                                                        | `ApiKey`, `CognitiveSearch`, `GroundingWithBingSearch`, `ContainerRegistry` |
| `CustomKeys`                 | `custom-keys`                  | `{ keys: { <header>: <string>, ... } }`                                    | `RemoteTool`, `CustomKeys` (MCP with multiple headers) |
| `OAuth2`                     | -                              | `{ clientId, clientSecret }` (+ optional `authUrl`, `tokenUrl`, `refreshUrl`, `scopes`, `tenantId`, `username`, `password`, `developerToken`, `refreshToken`) | `RemoteTool` (OAuth-protected MCP / OpenAPI)      |
| `UserEntraToken`             | `user-entra-token`             | none -- token from the END USER's Entra session. `audience:` required.     | `RemoteTool` (1P MCP server needing OBO)          |
| `AgenticIdentity`            | `agentic-identity`             | none -- token from the AGENT's identity. `audience:` required.             | `RemoteTool` (downstream service trusting the agent's MI) |
| `ProjectManagedIdentity`     | `project-managed-identity`     | none -- token from the Foundry project's MI. `audience:` optional.         | `RemoteTool`, A2A                                 |
| `PAT`                        | -                              | `{ pat: <string> }`                                                        | `Git`                                             |
| `AAD`                        | -                              | none -- AAD principal resolved from caller. `audience:` optional.          | `CognitiveSearch`, `AzureOpenAI`, `AzureBlob`     |
| `ServicePrincipal`           | -                              | `{ clientId, clientSecret, tenantId }`                                     | `RemoteTool`, downstream Azure services           |
| `UsernamePassword`           | -                              | `{ username, password }`                                                   | `Redis`, `Snowflake`, `AzureSqlDb`                |
| `AccessKey` / `AccountKey`   | -                              | `{ key: <string> }`                                                        | `AzureBlob`, `ADLSGen2`                           |
| `SAS`                        | -                              | `{ sas: <string> }`                                                        | `AzureBlob`                                       |
| `None`                       | `none`                         | omit `credentials:` entirely                                               | Anonymous endpoints                               |

For auth types without a slug, pass ARM-canonical to `--auth-type` or hand-edit `azure.yaml`.

## `credentials.type:` promotion

Manifests can put the auth type inside the credentials map instead of at the top level. Init promotes it:

```yaml
# Both forms produce the same azure.yaml
- kind: connection
  name: my-conn
  credentials:
    type: CustomKeys
    keys:
      Authorization: "Bearer {{ pat }}"

# OR
- kind: connection
  name: my-conn
  authType: CustomKeys
  credentials:
    keys:
      Authorization: "Bearer {{ pat }}"
```

If both are set, `authType:` wins. The `type:` key is removed from `credentials:` during promotion so it doesn't end up as a fake `PARAM_*` env var.

When writing `azure.yaml` directly, always put `authType:` at the top level.

## Credential externalization (`PARAM_*` env vars)

Every string leaf in `credentials:` is externalized at init time. The rule:

1. For each `credentials.<key>: "<value>"` (and nested paths), init computes a name: `PARAM_<UPPER_CONN_NAME>_<UPPER_KEY_PATH>`. Non-alphanumeric characters become `_`.
2. The raw value goes into `azd env set PARAM_<...> <value>` for the active environment.
3. The value in `azure.yaml` becomes `${PARAM_<...>}`.

Examples:

| Manifest input                                                            | azd env var set                                              | `azure.yaml` value                                          |
| ------------------------------------------------------------------------- | ------------------------------------------------------------ | ----------------------------------------------------------- |
| `credentials.key: "abc123"` on connection `my-search`                     | `PARAM_MY_SEARCH_KEY=abc123`                                 | `key: ${PARAM_MY_SEARCH_KEY}`                               |
| `credentials.keys.Authorization: "Bearer xyz"` on `github-mcp-conn`       | `PARAM_GITHUB_MCP_CONN_KEYS_AUTHORIZATION=Bearer xyz`        | `Authorization: ${PARAM_GITHUB_MCP_CONN_KEYS_AUTHORIZATION}`|
| `credentials.clientId: "id"` + `clientSecret: "sec"` on `my-oauth`        | `PARAM_MY_OAUTH_CLIENTID=id`, `PARAM_MY_OAUTH_CLIENTSECRET=sec` | matching `${PARAM_...}` references                       |

Nested maps preserve structure -- the env name accumulates the path:

```yaml
credentials:
  keys:
    Authorization: "Bearer ..."      # -> PARAM_<CONN>_KEYS_AUTHORIZATION
    X-Tenant: "contoso"              # -> PARAM_<CONN>_KEYS_X_TENANT
```

`azure.yaml` is in source control; raw secrets cannot live there. The `<env>/.env` file is gitignored -- that's where actual values go.

### Setting credentials manually

When you add a connection post-init:

```bash
# 1. Write azure.yaml with ${PARAM_<...>} placeholder.
# 2. Set the value.
azd env set PARAM_MY_NEW_CONN_KEY "<actual-secret>"
# 3. azd provision.
```

`azd env set` writes to `<env>/.env`. List with `azd env list`.

## When credentials are not needed

`UserEntraToken`, `AgenticIdentity`, `ProjectManagedIdentity`, `AAD`, `None`: omit `credentials:`. The first three need `audience:`.

```yaml
# UserEntraToken (1P OBO)
- name: workiq-mail-conn
  category: RemoteTool
  target: https://agent365.svc.cloud.microsoft/agents/servers/mcp_MailTools
  authType: UserEntraToken
  audience: ea9ffc3e-8a23-4a7d-836d-234d7c7565c1

# ProjectManagedIdentity
- name: peer-agent-conn
  category: RemoteTool
  target: https://other-agent.foundry.azure.com/
  authType: ProjectManagedIdentity
  audience: https://ai.azure.com/.default

# None
- name: public-mcp-conn
  category: RemoteTool
  target: https://example.com/mcp
  authType: None
```

`UserEntraToken` and `AgenticIdentity` require the right Entra app registration / role assignment on the Foundry project. Usually a one-time setup outside this extension.

## OAuth2 details

### Your own OAuth app (client-credentials flow)

```yaml
- name: my-oauth-conn
  category: RemoteTool
  target: https://api.example.com/mcp
  authType: OAuth2
  credentials:
    clientId: ${PARAM_MY_OAUTH_CONN_CLIENTID}
    clientSecret: ${PARAM_MY_OAUTH_CONN_CLIENTSECRET}
  authorizationUrl: https://login.example.com/oauth/authorize
  tokenUrl: https://login.example.com/oauth/token
  refreshUrl: https://login.example.com/oauth/refresh
  scopes: [read, write]
```

CLI:

```bash
azd ai agent connection create my-oauth-conn \
  --kind remote-tool \
  --target https://api.example.com/mcp \
  --auth-type oauth2 \
  --client-id "<id>" \
  --client-secret "<secret>"
```

### Foundry-managed OAuth (Microsoft hosts the app)

```yaml
- name: github-oauth-conn
  category: RemoteTool
  target: https://api.githubcopilot.com/mcp
  authType: OAuth2
  connectorName: foundrygithubmcp
  credentials:
    type: OAuth2     # required, no clientId/clientSecret needed
```

End users complete the OAuth handshake on first call. No client credentials.

## Validate

```bash
azd ai agent doctor --output json
```

Look for `remote.connections` and `local.agent-yaml-valid`. Failures happen when:

* `authType:` value isn't recognized.
* `credentials:` is missing fields the `authType:` needs.
* `audience:` missing for `UserEntraToken` / `AgenticIdentity`.
* A `${PARAM_*}` reference points at an unset env var.
