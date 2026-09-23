# Inspect and edit agent State Stores

Foundry State Stores hold application JSON data such as checkpoints. Use `azd ai agent state-stores` to inspect **existing stores** and manage their items. Create stores in agent code or other tooling first; see the public [Foundry State Store documentation](https://learn.microsoft.com/en-us/azure/foundry/agents/concepts/agent-state-store?tabs=python).

Editing state does not stop, resume, or steer agent work. Coordinate changes with the application, especially when modifying active checkpoints.

## Commands

```text
azd ai agent state-stores list [--limit <count>] [--order asc|desc] [--after <cursor>]
azd ai agent state-stores select [store-name]
azd ai agent state-stores show [store-name]
azd ai agent state-stores items list [--store <name>] [--limit <count>] [--order asc|desc] [--after <cursor>]
azd ai agent state-stores items show <key> [--store <name>]
azd ai agent state-stores items set <key> [--store <name>] (--value <json-object> | --value-file <path|->) [--tag <key=value>]... [--if-match <etag>]
azd ai agent state-stores items delete <key> [--store <name>] [--if-match <etag>] [--yes]
```

Every command supports `--agent-name <service-name>` or `--agent-endpoint <https-protocol-endpoint-url>`, but not both. The agent is otherwise resolved from the azd project/environment. `--environment <name>` reads deployment metadata from that environment without changing the project's default environment; missing metadata never falls back to another environment. Do not combine an explicitly supplied `--environment` with `--agent-endpoint`: the URL determines the target and does not use environment metadata. Explicit HTTPS endpoint targeting also works outside a project. WebSocket (`wss://.../invocations_ws`) invocation URLs are not accepted as State Store targets; State Store traffic uses HTTPS, not WebSockets. For a WebSocket agent, target it from an azd project by service name. State Stores are independent of invocation protocol and agent version; no protocol or version flag is needed.

Pass logical store names and keys, including embedded `/`, without encoding them. azd handles the API's base64url encoding. Commands use normal azd authentication and the external agent-scoped State Store API; they do not supply delegated identity or hosted-only call headers.

## Select a store and inspect items

```bash
azd ai agent state-stores list --output table
azd ai agent state-stores select "checkpoints/run-42"
azd ai agent state-stores show
azd ai agent state-stores items list
azd ai agent state-stores items show "task-123"

# Inspect another store without changing the active selection
azd ai agent state-stores items show "task-456" --store "checkpoints/run-43"
```

`select` without a name opens a picker with a next-page choice when needed. Under `--no-prompt`, supply a name. Selection is validated before saving.

Only `select` changes the active store. Its name is saved under `extensions.ai-agents.stateStores` in the azd user configuration (`~/.azd/config.json`, or `$AZD_CONFIG_DIR/config.json`). Keys identify the project endpoint and deployed agent name, not the version or protocol. Two local projects targeting the same remote agent share this selection. Item values, ETags, and credentials are not saved as selection state.

A missing selection or inaccessible store produces an error; azd never automatically creates or switches stores.

## Write and delete items

```bash
# One PUT: create a missing item or replace an existing one
azd ai agent state-stores items set "test-checkpoint" \
  --value '{"step":1,"status":"pending"}' --tag kind=checkpoint

# checkpoint.json contains only the JSON object value, not a request envelope.
# The example uses jq to extract the ETag, preserving its quotes.
ETAG=$(azd ai agent state-stores items show "test-checkpoint" | jq -r '.etag')
azd ai agent state-stores items set "test-checkpoint" \
  --value-file checkpoint.json --tag kind=checkpoint --if-match "$ETAG"

azd ai agent state-stores items delete "test-checkpoint" --yes
```

- Supply exactly one value source. `--value-file -` reads stdin. The top-level value must be a JSON **object**; nested arrays, scalars, and null are allowed. JSON numbers retain their precision.
- The [public preview service limit](https://learn.microsoft.com/en-us/azure/foundry/agents/concepts/agent-state-store?tabs=python#service-limits) is **1 MB of serialized JSON per value**. The CLI uses the service's byte ceiling of **1,048,576 bytes (1 MiB)** and checks the value as it will be serialized, including JSON escaping but excluding the request envelope and tags. Undocumented larger-value support is not assumed.
- The CLI derives its raw-input guard from that same byte ceiling instead of applying an arbitrary multiplier. This conservative bound includes whitespace in inline, file, and stdin input: a formatted file larger than 1 MiB must be compacted externally first, even if its compact value would fit. If the compact value is still too large, reduce it or store large content elsewhere and save a reference. Oversized input is rejected before a State Store request, never truncated into a write.
- `set` replaces the **complete value and tag map**, without an existence probe. **Omitting `--tag` clears existing tags.** Tags are strings, split at the first `=`; empty values are allowed, but empty or duplicate keys are rejected. The CLI checks the published limits of at most 16 tags, 64 characters per key, and 256 characters per value before writing.
- `--if-match` on set/delete passes the quoted ETag unchanged. A stale ETag fails with HTTP 412. azd never removes the condition or automatically retries writes after a lost response.
- Deletion confirms the target unless `--yes` is supplied. `--no-prompt` requires `--yes`. An already absent item can return a successful deletion tombstone; a missing store or other service error still fails.

## Sensitive values and diagnostics

Prefer `--value-file <path>` or `--value-file -` for sensitive values, and avoid `--debug` when passing sensitive inline values or tags. Some azd host versions log extension command-line arguments, including `--value` and `--tag`, even though the State Store HTTP client does not log request or response bodies.

## Output and pagination

JSON is the default; use `--output table` for readable output. Item lists return metadata, not values. Item `show` includes the value, tags, and ETag. Write responses contain service metadata and may omit the value or tags; azd preserves those omissions and does not fetch the item again.

List commands return at most `--limit` results per page. The default is **20**, with the service-supported range **1–100**. `--order` defaults to **desc**, following service ordering rather than alphabetical names.

Omit `--after` for the first page. When `has_more` is true, pass the response's `last_id` to `--after` to continue, keeping the same order and limit. The cursor entry itself is excluded. `--after` advances through either ascending or descending order; it is not a numeric offset.

Pass `last_id` unchanged. The service currently returns a store name or item key as the cursor, **not** an entry's `id` (`ss_...` or `it_...`). Do not base64url-encode the cursor. JSON preserves `data`, `first_id`, `last_id`, and `has_more`; table output provides the next cursor. The CLI supports forward pagination only, with no automatic traversal or `--all`.

These Bash examples use `jq` to read the cursor. Fetch the first store page and, if available, the next one:

```bash
PAGE=$(azd ai agent state-stores list --limit 2 --order asc)
printf '%s\n' "$PAGE"
if [ "$(printf '%s' "$PAGE" | jq -r '.has_more')" = "true" ]; then
  CURSOR=$(printf '%s' "$PAGE" | jq -r '.last_id')
  azd ai agent state-stores list --limit 2 --order asc --after "$CURSOR"
fi
```

The same flow lists items in the selected store:

```bash
PAGE=$(azd ai agent state-stores items list --limit 2 --order asc)
printf '%s\n' "$PAGE"
if [ "$(printf '%s' "$PAGE" | jq -r '.has_more')" = "true" ]; then
  CURSOR=$(printf '%s' "$PAGE" | jq -r '.last_id')
  azd ai agent state-stores items list --limit 2 --order asc --after "$CURSOR"
fi
```

Store creation/update/deletion, create-only item writes, bulk operations, and list-time tag filtering are outside this command set. Tag filtering is deferred because the preview service can reject valid tag keys or return incorrect matches.
