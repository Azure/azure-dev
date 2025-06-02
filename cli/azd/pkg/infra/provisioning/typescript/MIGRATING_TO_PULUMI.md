# Migrating from azd TypeScript Provider to Pulumi

This guide helps you migrate your Azure Developer CLI (azd) TypeScript-based infrastructure code to Pulumi, so you can leverage Pulumi’s ecosystem, state management, and multi-cloud capabilities.

---

## 1. Entrypoint and Project Structure

**azd TypeScript Provider:**
- Entrypoint: `infra/deploy.ts`
- Exports a `deploy(context)` function
- Outputs are returned as a JSON object

**Pulumi:**
- Entrypoint: `index.ts` (or any file referenced in `Pulumi.yaml`)
- Uses top-level imperative resource declarations
- Outputs are set using `pulumi.export`

---

## 2. Example Comparison

### azd TypeScript Provider (`infra/deploy.ts`)
```ts
import { ResourceGroups } from '@azure/arm-resources';
import { DefaultAzureCredential } from '@azure/identity';

export async function deploy(context) {
  const credential = new DefaultAzureCredential();
  const rgClient = new ResourceGroups(credential, context.subscriptionId);
  await rgClient.createOrUpdate(context.resourceGroup, { location: context.location });
  // ...provision more resources
  return {
    outputs: {
      websiteUrl: 'https://myapp.azurewebsites.net',
    },
  };
}
```

### Pulumi (`index.ts`)
```ts
import * as azure_native from '@pulumi/azure-native';

const resourceGroup = new azure_native.resources.ResourceGroup('my-rg', {
  location: 'eastus',
});

// ...provision more resources

export const websiteUrl = 'https://myapp.azurewebsites.net';
```

---

## 3. Migration Steps

1. **Copy Resource Logic**
   - Move your Azure SDK resource creation logic from `deploy.ts` to `index.ts`.
   - Replace direct SDK calls with Pulumi resource classes (e.g., `new azure_native.resources.ResourceGroup`).

2. **Replace Context**
   - Instead of using a `context` parameter, use Pulumi config and stack outputs for dynamic values.
   - Use Pulumi’s `pulumi.Config` to read configuration.

3. **Outputs**
   - Replace the `outputs` return object with `export const ...` in Pulumi.

4. **State Management**
   - Pulumi manages state automatically (local, cloud, or self-hosted backend).
   - No need to serialize outputs as JSON.

5. **CLI Usage**
   - Use `pulumi up` to deploy, `pulumi destroy` to tear down, and `pulumi preview` to see changes.

---

## 4. Example Migration

**azd deploy.ts:**
```ts
export async function deploy(context) {
  // ...
  return { outputs: { foo: 'bar' } };
}
```

**Pulumi index.ts:**
```ts
export const foo = 'bar';
```

---

## 5. Tips
- Use Pulumi’s [Azure Native provider](https://www.pulumi.com/registry/packages/azure-native/) for best compatibility.
- Use `pulumi.Config` for environment/config values.
- Outputs in Pulumi are always available via the CLI and the Pulumi Service.
- Pulumi supports TypeScript, JavaScript, Python, Go, .NET, and YAML.

---

## 6. Resources
- [Pulumi Azure Native Docs](https://www.pulumi.com/registry/packages/azure-native/)
- [Pulumi Getting Started](https://www.pulumi.com/docs/get-started/azure/)
- [Pulumi Migration Guides](https://www.pulumi.com/docs/guides/adopting-pulumi/)

---

**Summary:**
- Most of your TypeScript resource logic is portable.
- You’ll need to adapt entrypoint, outputs, and state handling.
- Pulumi’s imperative, top-level resource model is similar to your current approach.
