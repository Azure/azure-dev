# Try private registry connections early

> [!CAUTION]
> This branch is temporary and unsupported. Build azd core and the agents extension from the same checkout. Do not use
> the locally built extension with another azd version. The branch may be removed after the feature ships.

This guide covers two scenarios:

1. **Greenfield:** initialize a new local azd project with a private registry.
2. **Brownfield:** add a private-registry agent to an existing local azd project.

The examples are registry-neutral. Replace every placeholder with values for your Foundry project and registry. Do not
commit credentials or environment files.

## Prerequisites

- Git and Go 1.26.4.
- Azure access to create, deploy, invoke, and delete agents on a Foundry project.
- A private image supported by a Foundry registry connection.
- Permission to create a connection on the Foundry project.
- The connection target, metadata, and credential values required by your registry.

### Brief JFrog OIDC setup

Skip this section if the registry connection already works. The feature does not configure JFrog or Entra ID for you.

1. Get the Foundry project's managed identity and tenant:

   ```bash
   export PROJECT_ID='/subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.CognitiveServices/accounts/<account>/projects/<project>'
   export PROJECT_PRINCIPAL_ID=$(az resource show --ids "$PROJECT_ID" \
     --api-version 2025-06-01 --query identity.principalId -o tsv)
   export PROJECT_TENANT_ID=$(az resource show --ids "$PROJECT_ID" \
     --api-version 2025-06-01 --query identity.tenantId -o tsv)
   ```

2. Create or select an Entra application to use as the token audience. Record its application (client) ID.
3. Create a JFrog OIDC integration with provider type **Azure**, issuer
   `https://sts.windows.net/<tenant-id>/`, the Entra application ID, and a unique provider name.
4. Add a JFrog identity mapping whose `oid` claim equals `PROJECT_PRINCIPAL_ID`. Map it to a dedicated JFrog user or
   group with read access only to the required Docker repository; do not use an administrator identity.
5. Create the Foundry project connection with the registry URL and these `CustomKeys` values:
   - `audience`: the Entra application ID
   - `tokenEndpoint`: `/access/api/v1/oidc/token`
   - `body.provider_name`: the JFrog OIDC provider name

Set connection metadata `type: registry_connection` and `mode: oauth_token_exchange`. Scenario 2 below shows the full
connection shape.

## Build matching azd core and extension binaries

Use an isolated azd configuration so this test does not replace extensions or authentication in your normal azd
installation.

```bash
git clone --branch m5i/private-registry-early-birds \
  https://github.com/Azure/azure-dev.git azure-dev-private-registry
cd azure-dev-private-registry

export EARLY_BIRDS_ROOT="$PWD"
export AZD_CONFIG_DIR="$HOME/.azd-private-registry-early-birds"
export AZURE_DEV_COLLECT_TELEMETRY=no
mkdir -p "$EARLY_BIRDS_ROOT/bin" "$AZD_CONFIG_DIR"

cd "$EARLY_BIRDS_ROOT/cli/azd"
AZD_VERSION=$(tr -d '\r\n' < "$EARLY_BIRDS_ROOT/cli/version.txt")
AZD_COMMIT=$(git -C "$EARLY_BIRDS_ROOT" rev-parse HEAD)
go build \
  -ldflags="-X 'github.com/azure/azure-dev/cli/azd/internal.Version=$AZD_VERSION (commit $AZD_COMMIT)'" \
  -o "$EARLY_BIRDS_ROOT/bin/azd"
export PATH="$EARLY_BIRDS_ROOT/bin:$PATH"
hash -r

command -v azd
azd version
azd auth login
azd ext install microsoft.azd.extensions --source azd --no-prompt
azd ext install azure.ai.inspector --source azd --no-prompt

cd "$EARLY_BIRDS_ROOT/cli/azd/extensions/azure.ai.projects"
azd x build
azd x pack
azd x publish
azd ext install azure.ai.projects --source local --force --no-prompt

cd "$EARLY_BIRDS_ROOT/cli/azd/extensions/azure.ai.agents"
azd x build
azd x pack
azd x publish
azd ext install azure.ai.agents --source local --force --no-prompt
azd ext list
```

Expected versions:

- azd core reports `1.32.0-beta.1` and the branch commit.
- `azure.ai.projects` reports `1.0.0-beta.6` and is installed from the local build.
- `azure.ai.agents` reports `1.0.0-beta.10` and is installed from the local build.
- The agents extension's temporary `replace` directive compiles it against core from this checkout.

In every new shell, set `EARLY_BIRDS_ROOT` and `AZD_CONFIG_DIR`, prepend `$EARLY_BIRDS_ROOT/bin` to `PATH`, and run
`hash -r` before using `azd`.

## Greenfield: initialize a new azd project with a private registry

This scenario starts in an empty directory. Create the Foundry connection through ARM before running init. The
`azd ai connection` command is intended for an existing azd project, so it cannot resolve project context yet.

```bash
export SUBSCRIPTION_ID='<subscription-id>'
export PROJECT_ID="/subscriptions/$SUBSCRIPTION_ID/resourceGroups/<resource-group>/providers/Microsoft.CognitiveServices/accounts/<account>/projects/<project>"
export CONNECTION_NAME='<unique-connection-name>'
export REGISTRY_URL='https://<registry-host>'
export REGISTRY_AUDIENCE='<entra-application-client-id>'
export REGISTRY_TOKEN_ENDPOINT='/access/api/v1/oidc/token'
export REGISTRY_PROVIDER='<jfrog-oidc-provider-name>'
export IMAGE='<registry-host>/<repository>/<image>:<tag>'
export AGENT_NAME='<unique-agent-name>'

cat > connection.json <<EOF
{
  "properties": {
    "category": "CustomKeys",
    "target": "$REGISTRY_URL",
    "authType": "CustomKeys",
    "credentials": {
      "keys": {
        "audience": "$REGISTRY_AUDIENCE",
        "tokenEndpoint": "$REGISTRY_TOKEN_ENDPOINT",
        "body.provider_name": "$REGISTRY_PROVIDER"
      }
    },
    "metadata": {
      "type": "registry_connection",
      "mode": "oauth_token_exchange"
    }
  }
}
EOF

az account set --subscription "$SUBSCRIPTION_ID"
az rest --method put \
  --url "https://management.azure.com${PROJECT_ID}/connections/${CONNECTION_NAME}?api-version=2025-04-01-preview" \
  --body @connection.json

mkdir -p "$EARLY_BIRDS_ROOT/.early-birds/greenfield"
cd "$EARLY_BIRDS_ROOT/.early-birds/greenfield"

azd ai agent init --no-prompt \
  --agent-name "$AGENT_NAME" \
  --image "$IMAGE" \
  --project-id "$PROJECT_ID" \
  --registry-connection "$CONNECTION_NAME" \
  --protocol invocations

cd "$AGENT_NAME"
azd deploy --no-prompt
printf '{"message":"Hello from the private registry early-birds test"}\n' > request.json
azd ai agent invoke "$AGENT_NAME" \
  --protocol invocations \
  --new-session \
  --input-file request.json
```

The generated agent service should contain:

```yaml
image: <registry-host>/<repository>/<image>:<tag>
docker:
  imagePassthrough: true
registryConnectionId: <unique-connection-name>
```

## Brownfield: add a private-registry agent to an existing azd project

This scenario starts with an existing local azd project, so use the friendlier connection command. Run it from the
project directory, then add the agent service to `azure.yaml`.

```bash
cd '<existing-azd-project-directory>'
export PROJECT_ENDPOINT='https://<account>.services.ai.azure.com/api/projects/<project>'

azd ai connection create private-registry \
  --project-endpoint "$PROJECT_ENDPOINT" \
  --kind custom-keys \
  --target 'https://<registry-host>' \
  --auth-type custom-keys \
  --custom-key 'audience=<entra-application-client-id>' \
  --custom-key 'tokenEndpoint=/access/api/v1/oidc/token' \
  --custom-key 'body.provider_name=<jfrog-oidc-provider-name>' \
  --metadata 'type=registry_connection' \
  --metadata 'mode=oauth_token_exchange' \
  --no-prompt
```

```yaml
infra:
  provider: microsoft.foundry

services:
  existing-foundry-project:
    host: azure.ai.project
    endpoint: https://<account>.services.ai.azure.com/api/projects/<project>

  private-registry-agent:
    host: azure.ai.agent
    uses:
      - existing-foundry-project
    kind: hosted
    name: private-registry-agent
    image: registry.example.com/team/agent:v1
    docker:
      imagePassthrough: true
    registryConnectionId: private-registry
    protocols:
      - protocol: invocations
        version: 1.0.0
```

Replace `registry.example.com/team/agent:v1` and the agent service name with unique values for your test. Then set the
project values in a dedicated azd environment, provision, and deploy:

```bash
cd '<existing-azd-project-directory>'

azd env new '<unique-environment-name>' --no-prompt
azd env set AZURE_SUBSCRIPTION_ID '<subscription-id>'
azd env set AZURE_TENANT_ID '<tenant-id>'
azd env set AZURE_LOCATION '<azure-region>'
azd env set AZURE_RESOURCE_GROUP '<existing-resource-group>'
azd env set AZURE_AI_PROJECT_ID '<foundry-project-resource-id>'
azd env set AZURE_AI_ACCOUNT_NAME '<account>'
azd env set AZURE_AI_PROJECT_NAME '<project>'
azd env set FOUNDRY_PROJECT_ENDPOINT 'https://<account>.services.ai.azure.com/api/projects/<project>'
azd env set USE_EXISTING_AI_PROJECT true

azd provision --no-prompt
azd deploy --no-prompt
printf '{"message":"Hello from the declarative private registry test"}\n' > request.json
azd ai agent invoke 'private-registry-agent' \
  --protocol invocations \
  --new-session \
  --input-file request.json
```

## Cleanup

Do not run `azd down` when the Foundry project is shared or existed before this test. Delete only the uniquely named
agents, sessions, and connections created for testing. Keep the local project directories until you have saved any
logs needed for troubleshooting.
