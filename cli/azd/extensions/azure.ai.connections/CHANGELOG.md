# Release History

## 0.0.1-preview (2026-05-28)

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