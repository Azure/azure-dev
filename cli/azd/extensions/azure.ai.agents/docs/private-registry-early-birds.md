# Try private registry connections early

> [!CAUTION]
> This branch is temporary and unsupported. Build azd core and the agents extension from the same checkout. Do not use
> the locally built extension with another azd version. The branch may be removed after the feature ships.

This guide covers two scenarios:

1. Initialize a new local azd project with an existing Foundry project and registry connection.
2. Add a private-image agent and a sibling registry connection to an existing local azd project.

The examples are registry-neutral. Replace every placeholder with values for your Foundry project and registry. Do not
commit credentials or environment files.

## Prerequisites

- Git and Go 1.26.4.
- Azure access to create, deploy, invoke, and delete agents on a Foundry project.
- A private image supported by a Foundry registry connection.
- For scenario 1, an existing registry connection on the Foundry project.
- For scenario 2, the connection target, metadata, and credential values required by your registry.

## Build matching azd core and extension binaries

Use an isolated azd configuration so this test does not replace extensions or authentication in your normal azd
installation.

```bash
git clone --branch m5i/private-registry-early-birds \
  https://github.com/m5i-work/azure-dev.git azure-dev-private-registry
cd azure-dev-private-registry

export EARLY_BIRDS_ROOT="$PWD"
export AZD_CONFIG_DIR="$HOME/.azd-private-registry-early-birds"
export AZURE_DEV_COLLECT_TELEMETRY=no
mkdir -p "$EARLY_BIRDS_ROOT/bin" "$AZD_CONFIG_DIR"

cd "$EARLY_BIRDS_ROOT/cli/azd"
go build -o "$EARLY_BIRDS_ROOT/bin/azd"
export AZD_EARLY_BIRDS="$EARLY_BIRDS_ROOT/bin/azd"

"$AZD_EARLY_BIRDS" version
"$AZD_EARLY_BIRDS" auth login
"$AZD_EARLY_BIRDS" ext install microsoft.azd.extensions --no-prompt
"$AZD_EARLY_BIRDS" ext install azure.ai.projects --no-prompt

cd "$EARLY_BIRDS_ROOT/cli/azd/extensions/azure.ai.agents"
"$AZD_EARLY_BIRDS" x build
"$AZD_EARLY_BIRDS" ext list
```

Expected versions:

- azd core reports `1.32.0-beta.1` and the branch commit.
- `azure.ai.agents` reports `1.0.0-beta.10` and is installed from the local build.
- The extension's temporary `replace` directive compiles it against core from this checkout.

Keep `AZD_CONFIG_DIR` and `AZD_EARLY_BIRDS` set in every shell used below.

## Scenario 1: initialize a new local azd project

This scenario starts in an empty directory. The Foundry project and registry connection must already exist.

```bash
export PROJECT_ID='/subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.CognitiveServices/accounts/<account>/projects/<project>'
export REGISTRY_CONNECTION='<existing-connection-name-or-id>'
export IMAGE='<registry-host>/<repository>/<image>:<tag>'
export AGENT_NAME='<unique-agent-name>'

mkdir -p "$EARLY_BIRDS_ROOT/.early-birds/greenfield"
cd "$EARLY_BIRDS_ROOT/.early-birds/greenfield"

"$AZD_EARLY_BIRDS" ai agent init --no-prompt \
  --agent-name "$AGENT_NAME" \
  --image "$IMAGE" \
  --project-id "$PROJECT_ID" \
  --registry-connection "$REGISTRY_CONNECTION"

"$AZD_EARLY_BIRDS" deploy --no-prompt
printf '{"message":"Hello from the private registry early-birds test"}\n' > request.json
"$AZD_EARLY_BIRDS" ai agent invoke "$AGENT_NAME" \
  --protocol invocations \
  --new-session \
  --input-file request.json
```

The generated agent service should contain:

```yaml
image: <registry-host>/<repository>/<image>:<tag>
docker:
  imagePassthrough: true
registryConnectionId: <existing-connection-name-or-id>
```

## Scenario 2: add a declarative agent and sibling connection

This scenario starts with an existing local azd project. Add services like the following to its `azure.yaml`. Use a
unique connection and agent name.

```yaml
infra:
  provider: microsoft.foundry

services:
  existing-foundry-project:
    host: azure.ai.project
    endpoint: ${FOUNDRY_PROJECT_ENDPOINT}

  private-registry-connection:
    host: azure.ai.connection
    uses:
      - existing-foundry-project
    category: CustomKeys
    target: ${REGISTRY_URL}
    authType: CustomKeys
    credentials:
      keys:
        audience: ${REGISTRY_AUDIENCE}
        tokenEndpoint: ${REGISTRY_TOKEN_ENDPOINT}
        body.provider_name: ${REGISTRY_PROVIDER}
    metadata:
      type: registry_connection
      mode: oauth_token_exchange

  private-registry-agent:
    host: azure.ai.agent
    uses:
      - existing-foundry-project
      - private-registry-connection
    kind: hosted
    name: private-registry-agent
    image: registry.example.com/team/agent:v1
    docker:
      imagePassthrough: true
    registryConnectionId: private-registry-connection
    protocols:
      - protocol: invocations
        version: 1.0.0
```

Replace `registry.example.com/team/agent:v1` and the static service names with unique values for your test. Then set the
project and registry values in a dedicated azd environment, provision, and deploy:

```bash
cd '<existing-azd-project-directory>'

"$AZD_EARLY_BIRDS" env new '<unique-environment-name>'
"$AZD_EARLY_BIRDS" env set FOUNDRY_PROJECT_ENDPOINT 'https://<account>.services.ai.azure.com/api/projects/<project>'
"$AZD_EARLY_BIRDS" env set REGISTRY_URL 'https://<registry-host>'
"$AZD_EARLY_BIRDS" env set REGISTRY_AUDIENCE '<registry-audience>'
"$AZD_EARLY_BIRDS" env set REGISTRY_TOKEN_ENDPOINT '<registry-token-endpoint>'
"$AZD_EARLY_BIRDS" env set REGISTRY_PROVIDER '<registry-provider-name>'

"$AZD_EARLY_BIRDS" provision --no-prompt
"$AZD_EARLY_BIRDS" deploy --no-prompt
printf '{"message":"Hello from the declarative private registry test"}\n' > request.json
"$AZD_EARLY_BIRDS" ai agent invoke 'private-registry-agent' \
  --protocol invocations \
  --new-session \
  --input-file request.json
```

## Cleanup

Do not run `azd down` when the Foundry project is shared or existed before this test. Delete only the uniquely named
agents, sessions, and connections created for testing. Keep the local project directories until you have saved any
logs needed for troubleshooting.
