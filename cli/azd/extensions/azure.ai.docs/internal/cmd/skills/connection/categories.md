---
short: Reference of connection categories (slug -> ARM-canonical mapping).
order: 30
---
# Connection categories

The value of `category:` in `azure.yaml connections[]` and `--kind` on `azd ai agent connection create`.

The CLI accepts kebab-case slugs (typing convenience) and ARM-canonical PascalCase. Both produce the same connection. `azure.yaml` is normalized to ARM-canonical on write.

## Common categories

| ARM-canonical (`category:`)  | CLI slug (`--kind`)            | Use for                                                                  |
| ---------------------------- | ------------------------------ | ------------------------------------------------------------------------ |
| `RemoteTool`                 | `remote-tool`                  | MCP server, OpenAPI tool, A2A peer agent. Most custom-tool work.         |
| `RemoteA2A`                  | `remote-a2a`                   | A2A peer (explicit; `RemoteTool` also works).                            |
| `CognitiveSearch`            | `cognitive-search`             | Azure AI Search service (for the `azure_ai_search` tool).                |
| `GroundingWithBingSearch`    | `grounding-with-bing-search`   | Bing search account (for the `bing_grounding` tool).                     |
| `BingLLMSearch`              | -                              | Newer Bing LLM-search (used by some `web_search` variants).              |
| `AIServices`                 | `ai-services`                  | Azure AI Services multi-service account.                                 |
| `AzureOpenAI`                | -                              | Azure OpenAI deployment (used by model resources).                       |
| `CognitiveService`           | -                              | Single-purpose Cognitive Service.                                        |
| `ApiKey`                     | `api-key`                      | Generic API-key-protected HTTP endpoint.                                 |
| `CustomKeys`                 | `custom-keys`                  | Endpoint needing multiple headers / params (e.g. Authorization + region).|
| `OAuth2`                     | (use `--auth-type`)            | OAuth2-protected endpoint.                                               |
| `AppInsights`                | `app-insights`                 | Application Insights (telemetry connections).                            |
| `ContainerRegistry`          | `container-registry`           | Azure Container Registry.                                                |
| `MicrosoftOneLake`           | -                              | OneLake workspace.                                                       |
| `AzureBlob` / `AzureSqlDb` / `AzureSynapseAnalytics` / `AzureMySqlDb` / `AzurePostgresDb` / `ADLSGen2` / `AzureDataExplorer` | - | Azure data services. |
| `Git`                        | -                              | Git repository (dataset versioning).                                     |
| `Redis`                      | -                              | Redis cache.                                                             |
| `S3`                         | -                              | AWS S3 bucket.                                                           |
| `Snowflake`                  | -                              | Snowflake warehouse.                                                     |
| `Serverless`                 | -                              | Foundry serverless model endpoint.                                       |
| `Elasticsearch` / `Pinecone` / `Qdrant` | -                   | Vector DBs.                                                              |

Categories without a slug: pass the ARM-canonical form to `--kind` directly (e.g. `--kind BingLLMSearch`). The slug list lives in `normalizeKind`; add a case there for new slugs.

## Pick one

* **MCP server** -> `RemoteTool`. Always.
* **Azure AI Search** -> `CognitiveSearch`. Pair with `azure_ai_search` (in `resources[]` outside a toolbox, or `toolboxes[].tools[]` inside).
* **Bing grounding** -> `GroundingWithBingSearch`. Pair with `bing_grounding`. For plain "search the web", use the built-in `web_search` tool in a toolbox -- no connection needed.
* **HTTP API with a static key in one header** -> `ApiKey`. Sends `Authorization: Bearer <key>` by default. For other header names or multiple headers, use `CustomKeys`.
* **HTTP API with multiple keyed headers** -> `CustomKeys`. Each entry in `credentials.keys` becomes a header.
* **OpenAPI backend** -> whichever auth its spec requires (`ApiKey`, `CustomKeys`, `OAuth2`). Pair with `openapi` in a toolbox.
* **Another deployed agent (A2A)** -> `RemoteTool` (or `RemoteA2A` if you want to be explicit).

## Cross-axis fields

* `authType` -- separate from `category`. Some combinations don't work (e.g. `CognitiveSearch` + `OAuth2`). See `auth-types`.
* `target` -- URL or ARM resource ID, depending on category.
* `metadata` -- category-specific. `CognitiveSearch` -> `indexName`. `Git` -> branch / ref. `Redis` -> port.

## Don't see your category?

* `azd ai agent connection create --kind <ARM-canonical>` passes the kind straight to ARM, so anything ARM accepts works.
* File an issue if a category needs better declarative support (recognized `metadata` keys, default credential shape).
