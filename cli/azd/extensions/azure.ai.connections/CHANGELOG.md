# Release History

## 1.0.0-beta.1 (2026-06-30)

### Features Added

- [[#8818]](https://github.com/Azure/azure-dev/pull/8818) The `azure.ai.connections` extension now registers an `azure.ai.connection` service-target host. `azd deploy`/`azd up` upsert each `host: azure.ai.connection` service in `azure.yaml` onto the Foundry project with an idempotent ARM CreateOrUpdate, expanding `${VAR}` secrets from the azd environment while passing Foundry server-side `${{...}}` expressions through untouched.
- [[#8890]](https://github.com/Azure/azure-dev/pull/8890) Bump `requiredAzdVersion` to `>=1.27.0`.

## 0.1.2-preview (2026-06-19)

### Bugs Fixed

- [[#8688]](https://github.com/Azure/azure-dev/issues/8688) Resolve the project endpoint that `azd ai agent init` stores. `azd ai connection` commands now fall back to `AZURE_AI_PROJECT_ENDPOINT` (after `FOUNDRY_PROJECT_ENDPOINT`) in both the active azd environment and the host environment, so the resolution cascade no longer fails with "no Foundry project endpoint resolved" when only the `AZURE_AI_*` key is set.

## 0.1.1-preview (2026-06-05)

### Features Added

- [[#8475]](https://github.com/Azure/azure-dev/pull/8475) Add `--metadata key=value` flag (repeatable) to `azd ai connection update`, allowing metadata to be set or merged on existing connections without recreating them. Also fixes the update path for OAuth2 connections: emit the credentials object in the PUT body (resolves HTTP 400 "Credentials Property can't be empty for auth type OAuth2") and preserve existing OAuth2 fields (connectorName, authorizationUrl, tokenUrl, scopes, etc.) on target/metadata-only updates.

### Bugs Fixed

- [[#8539]](https://github.com/Azure/azure-dev/pull/8539) Remove leaked debug log from `resolveConnectionContext` that surfaced the resolved endpoint and source as user-visible noise on every `azd ai connection` invocation.

## 0.1.0-preview (2026-05-28)

Initial release of the `azure.ai.connections` extension. Provides CRUD management
of Microsoft Foundry project connections directly from the terminal.

- `azd ai connection list` — List all connections in the active Foundry project.
  Supports `--kind` to filter by connection kind (e.g. `remote-tool`,
  `cognitive-search`) and `--output json` or `--output table` (default).
- `azd ai connection show <name>` — Show details of a single connection including
  kind, auth type, target URL, and metadata. Pass `--show-credentials` to also
  retrieve secret credential values from the data plane.
- `azd ai connection create <name>` — Create a new connection. Requires `--kind`
  and `--target`. Supported auth types via `--auth-type`:
  - `none` (default) — no credentials.
  - `api-key` — supply the key with `--key`.
  - `custom-keys` — supply one or more `--custom-key key=value` pairs.
  - `oauth2` — either a managed connector (`--connector-name`) or BYO OAuth2
    (`--authorization-url`, `--token-url`, `--client-id`, `--client-secret`, and
    optionally `--refresh-url` and `--scopes`).
  - `user-entra-token`, `project-managed-identity`, `agentic-identity` — token
    auth types, with optional `--audience`.
  - Use `--force` to replace an existing connection (upsert).
- `azd ai connection update <name>` — Update a connection's `--target` URL or
  credential values (`--key` / `--custom-key`) in-place. All other fields are
  preserved; delete and recreate to change auth type.
- `azd ai connection delete <name>` — Delete a connection with an interactive
  confirmation prompt. Use `--force` to skip the prompt in non-interactive
  environments.
- Project-endpoint resolution cascade: `-p` / `--project-endpoint` flag →
  active azd environment `FOUNDRY_PROJECT_ENDPOINT` → global azd config →
  host environment variable `FOUNDRY_PROJECT_ENDPOINT`.