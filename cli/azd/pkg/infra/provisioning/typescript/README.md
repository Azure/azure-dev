# TypeScript Infra Provider for Azure Developer CLI

This provider enables Pulumi-style, enterprise-grade infrastructure as code using TypeScript for the Azure Developer CLI (`azd`).

## Key Features
- **SDK-based, versionable infra**: Write your infra in `infra/deploy.ts` using the Azure SDK for JavaScript/TypeScript.
- **No Bicep or Terraform**: Pure TypeScript and JSON, no Bicep or Terraform required.
- **Pulumi-style workflow**: Code, preview, deploy, and destroy using familiar patterns.
- **CI/CD Ready**: Can be triggered from GitHub Actions or other CI/CD systems.
- **Remote State**: Deployment state is tracked and versioned.

## Usage Example

### 1. Project Structure
```
myproject/
├── azure.yaml
└── infra/
    └── deploy.ts
```

### 2. Example `deploy.ts`
```ts
import { ResourceGroup, WebSite } from '@azure/arm-resources';
import { DefaultAzureCredential } from '@azure/identity';

export async function deploy(context) {
  const credential = new DefaultAzureCredential();
  const rgClient = new ResourceGroup(credential, context.subscriptionId);
  await rgClient.createOrUpdate(context.resourceGroup, { location: context.location });
  // ...provision more resources
  return {
    outputs: {
      websiteUrl: 'https://myapp.azurewebsites.net',
    },
  };
}
```

### 3. azd CLI Provider Selection
The CLI will use this provider if `infra/deploy.ts` exists:
```go
if exists("infra/deploy.ts") {
    provider = sdk.NewSDKInfraProvider("infra/deploy.ts")
} else {
    provider = bicep.NewBicepProvider(...)
}
```

### 4. GitHub Actions Example
```yaml
# .github/workflows/infra.yml
name: Deploy Infra
on:
  push:
    paths:
      - 'infra/**'
      - '.github/workflows/infra.yml'
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-node@v3
        with:
          node-version: '20'
      - run: npm ci
        working-directory: infra
      - run: npx ts-node deploy.ts
        working-directory: infra
        env:
          AZURE_SUBSCRIPTION_ID: ${{ secrets.AZURE_SUBSCRIPTION_ID }}
          AZURE_CLIENT_ID: ${{ secrets.AZURE_CLIENT_ID }}
          AZURE_TENANT_ID: ${{ secrets.AZURE_TENANT_ID }}
          AZURE_CLIENT_SECRET: ${{ secrets.AZURE_CLIENT_SECRET }}
```

## Patterns
- Follows the same Go interface patterns as other providers (see `bicep_provider.go`).
- All provider methods (`Deploy`, `Preview`, `Destroy`, etc.) are implemented in Go and invoke the TypeScript entrypoint.
- State and outputs are serialized as JSON.

## Testing
See `typescript_provider_test.go` for provider tests using Go mocks.
