---
short: Imperative CLI for connections (list, show, create, update, delete).
order: 50
---
# Connection management (imperative CLI)

`azd ai agent connection` commands target the Foundry project directly. They do **not** touch `azure.yaml`. For the declarative path (connections defined in `azure.yaml` and provisioned via Bicep), see `overview` and `add`.

> The connection commands live under `azd ai agent connection ...` today. They will eventually move to `azd ai connection ...` (currently a stub in the `azure.ai.connections` extension). Use the agent-namespaced form for now.

## When to use this

Imperative is right when:

* You're experimenting and don't want re-creation on every `azd provision`.
* The connection is centrally owned (e.g. shared AI Search service).
* You're adding a connection after provisioning and don't want to re-run provision for one thing.
* You're scripting a one-off ops task (key rotation, target change).

Declarative (`azure.yaml`) is right when:

* The connection is project-owned and should reproduce on every fresh provision.
* Secrets should live in source control as `${PARAM_*}` references with values in the env file.
* The connection should disappear on `azd down`.

## List

```bash
azd ai agent connection list --output json
azd ai agent connection list --kind cognitive-search --output json
```

Returns `[{name, kind, authType, target}, ...]`. `--kind` filters server-side. Accepts slugs or ARM-canonical.

## Show

```bash
azd ai agent connection show <name> --output json
azd ai agent connection show <name> --show-credentials --output json
```

`--show-credentials` fetches the raw secret values (data-plane response). Use this to recover a key. Output goes to stdout only; the CLI never persists it.

## Create

```bash
azd ai agent connection create <name> \
  --kind <category> \
  --target <url-or-arm-id> \
  --auth-type <auth-type> \
  [auth-specific flags]
```

Per-auth-type:

```bash
# ApiKey
azd ai agent connection create my-search \
  --kind cognitive-search \
  --target https://my-search.search.windows.net/ \
  --auth-type api-key \
  --key "<key>"

# CustomKeys (repeatable --custom-key)
azd ai agent connection create my-mcp \
  --kind remote-tool \
  --target https://api.example.com/mcp \
  --auth-type custom-keys \
  --custom-key Authorization="Bearer xyz" \
  --custom-key X-Tenant=contoso

# OAuth2
azd ai agent connection create my-oauth-mcp \
  --kind remote-tool \
  --target https://api.example.com/mcp \
  --auth-type oauth2 \
  --client-id "<id>" \
  --client-secret "<secret>"

# UserEntraToken (audience required)
azd ai agent connection create workiq-mail \
  --kind remote-tool \
  --target https://agent365.svc.cloud.microsoft/agents/servers/mcp_MailTools \
  --auth-type user-entra-token \
  --audience ea9ffc3e-8a23-4a7d-836d-234d7c7565c1

# AgenticIdentity (audience required)
azd ai agent connection create downstream-svc \
  --kind remote-tool \
  --target https://internal.contoso.com/api \
  --auth-type agentic-identity \
  --audience https://contoso.com/.default

# ProjectManagedIdentity (audience optional)
azd ai agent connection create peer-agent \
  --kind remote-tool \
  --target https://other-agent.foundry.azure.com/ \
  --auth-type project-managed-identity

# None (anonymous)
azd ai agent connection create public-mcp \
  --kind remote-tool \
  --target https://example.com/mcp \
  --auth-type none

# With metadata (repeatable)
azd ai agent connection create my-search \
  --kind cognitive-search \
  --target https://my-search.search.windows.net/ \
  --auth-type api-key --key "<key>" \
  --metadata indexName=docs-corpus \
  --metadata environment=prod
```

Replace an existing connection with `--force` (upsert):

```bash
azd ai agent connection create my-search \
  --kind cognitive-search --target ... --auth-type api-key --key "<new-key>" \
  --force
```

Without `--force`, an existing name fails with `connection_already_exists`.

## Flags

| Flag                        | Required when                       | What it does                                                                |
| --------------------------- | ----------------------------------- | --------------------------------------------------------------------------- |
| `--kind <category>`         | always                              | Slug or ARM-canonical. See `categories`.                                    |
| `--target <url>`            | always                              | Endpoint URL or ARM resource ID.                                            |
| `--auth-type <type>`        | always (defaults to `none`)         | `api-key`, `custom-keys`, `none`, `oauth2`, `user-entra-token`, `project-managed-identity`, `agentic-identity`. |
| `--key <value>`             | `--auth-type api-key`               | API key value.                                                              |
| `--custom-key k=v`          | `--auth-type custom-keys`           | Repeatable. Each becomes a header / per-category KV.                        |
| `--client-id <id>`          | `--auth-type oauth2`                | OAuth2 client ID.                                                           |
| `--client-secret <s>`       | `--auth-type oauth2`                | OAuth2 client secret.                                                       |
| `--audience <value>`        | `user-entra-token` / `agentic-identity` | Token audience (downstream app ID URI or `https://<host>/.default`).    |
| `--metadata k=v`            | optional                            | Repeatable. Category-specific (e.g. `indexName=...`).                       |
| `--force`                   | optional                            | `create`: upsert. `delete`: skip y/n prompt.                                |
| `-p, --project-endpoint`    | optional                            | Override the Foundry project endpoint. Falls back to `AZURE_AI_PROJECT_ENDPOINT` then azd config. |
| `-o, --output table\|json`  | optional                            | Defaults to `table`.                                                        |

## Update

Partial update -- only the specified fields change. Existing credentials are fetched and merged so you don't clobber them.

```bash
# Change target only
azd ai agent connection update my-search --target https://my-search-2.search.windows.net/

# Rotate the API key
azd ai agent connection update my-search --key "<new-key>"

# Update one custom-keys entry (keeps the rest)
azd ai agent connection update my-mcp --custom-key Authorization="Bearer new-token"
```

Needs at least one of `--target`, `--key`, `--custom-key` -- otherwise `missing_connection_field`.

`update` cannot change `--kind` or `--auth-type`. Delete and re-create for those.

## Delete

```bash
azd ai agent connection delete my-search           # interactive
azd ai agent connection delete my-search --force   # non-interactive
```

Removes the connection from the Foundry project. Any tool that referenced it by `project_connection_id` fails at call time until you remove or re-point the reference. Audit with `azd ai agent doctor --output json`.

If the connection was declared in `azure.yaml`, the next `azd provision` re-creates it. Delete the entry from `azure.yaml` first if you want it gone for good.

## Common error codes

* `connection_already_exists` -- `create` without `--force` against an existing name.
* `missing_connection_field` -- `update` with no `--target` / `--key` / `--custom-key`, or `create` missing a required flag for the auth type.
* `conflicting_arguments` -- e.g. `--audience` with the wrong auth type, or `--client-id` without `--auth-type oauth2`.
* `invalid_connection` -- ARM rejected the connection (target unreachable, credentials malformed, category not supported by the project tier).

## Confirmation envelope status

The connection CLI does **not** yet emit `confirmation_required` envelopes -- it uses a simpler `--force` flag for non-interactive runs.

When you're driving it in agent mode, get the developer's consent out-of-band (you ask, they reply), then re-run with `--force`.
