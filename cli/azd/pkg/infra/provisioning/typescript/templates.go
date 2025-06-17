// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package typescript

import (
	"path/filepath"
)

// GetTemplateFiles returns all template files needed for TypeScript infrastructure
func GetTemplateFiles(infraDir string) map[string][]byte {
	files := make(map[string][]byte)

	// Add all template files
	files[filepath.Join(infraDir, "src", "deploy.ts")] = []byte(DeployTsTemplate)
  files[filepath.Join(infraDir, "src", "destroy.ts")] = []byte(DestroyTsTemplate)
	files[filepath.Join(infraDir, "package.json")] = []byte(PackageJsonTemplate)
	files[filepath.Join(infraDir, "tsconfig.json")] = []byte(TsconfigJsonTemplate)
	files[filepath.Join(infraDir, "tsconfig.build.json")] = []byte(TsconfigBuildJsonTemplate)
	// Add azdConfig.json to infra and dist
	files[filepath.Join(infraDir, "config", "azd.config.json")] = []byte(AzdConfigTemplate)
	files[filepath.Join(infraDir, "dist", "config", "azd.config.json")] = []byte(AzdConfigTemplate)

	return files
}

// DeployTsTemplate is the main TypeScript infrastructure template
const DeployTsTemplate = `import { DefaultAzureCredential } from "@azure/identity";
import { ResourceManagementClient } from "@azure/arm-resources";
import { ContainerAppsAPIClient } from "@azure/arm-appcontainers";
import { ContainerRegistryManagementClient } from "@azure/arm-containerregistry";
import { CognitiveServicesManagementClient } from "@azure/arm-cognitiveservices";
import { OperationalInsightsManagementClient } from "@azure/arm-operationalinsights";
import { ApplicationInsightsManagementClient } from "@azure/arm-appinsights";
import * as fs from "fs";
import * as https from "https";
import { execSync } from "child_process";

const subscriptionId = process.env.AZURE_SUBSCRIPTION_ID!;
const environmentName = process.env.AZURE_ENV_NAME!;
const location = process.env.AZURE_LOCATION!;
const principalId = process.env.AZURE_PRINCIPAL_ID!;
const serviceName = process.env.AZURE_SERVICE_NAME || "llama-index-javascript";

const azdConfig = require('./config/azd.config.json');

async function waitForManagedEnvReady(rgName: string, envName: string, containerAppsClient: ContainerAppsAPIClient) {
  const maxRetries = 30;
  const delayMs = 5000;
  for (let i = 0; i < maxRetries; i++) {
    const envStatus = await containerAppsClient.managedEnvironments.get(rgName, envName);
    if (envStatus.provisioningState === "Succeeded" || envStatus.provisioningState === "Ready") {
      return;
    }
    console.log("Waiting for managed environment to be ready... current state: " + envStatus.provisioningState);
    await new Promise(r => setTimeout(r, delayMs));
  }
  throw new Error("Timed out waiting for managed environment to be ready");
}

async function httpsPut(url: string, token: string, body: any): Promise<void> {
  return new Promise((resolve, reject) => {
    const data = JSON.stringify(body);
    const parsedUrl = new URL(url);
    const options: https.RequestOptions = {
      hostname: parsedUrl.hostname,
      path: parsedUrl.pathname + parsedUrl.search,
      method: "PUT",
      headers: {
        "Authorization": "Bearer " + token,
        "Content-Type": "application/json",
        "Content-Length": Buffer.byteLength(data)
      }
    };

    const req = https.request(options, (res) => {
      let resData = "";
      res.on("data", chunk => resData += chunk);
      res.on("end", () => {
        if (res.statusCode && res.statusCode >= 200 && res.statusCode < 300) {
          resolve();
        } else {
          reject(new Error("Request failed with status " + res.statusCode + ": " + resData));
        }
      });
    });

    req.on("error", reject);
    req.write(data);
    req.end();
  });
}

async function deployModel(deployment: string, model: string, version: string, capacity: number) {
  const aoaiName = "aoai-" + environmentName;
  const rgName = "rg-" + environmentName;
  const token = (await new DefaultAzureCredential().getToken("https://management.azure.com/.default"))?.token;
  if (!token) throw new Error("Failed to get token");

  const url = [
    "https://management.azure.com",
    "subscriptions", subscriptionId,
    "resourceGroups", rgName,
    "providers", "Microsoft.CognitiveServices",
    "accounts", aoaiName,
    "deployments", deployment
  ].join("/") + "?api-version=2023-10-01-preview";

  const body = {
    sku: { name: "Standard", capacity },
    properties: {
      model: {
        format: "OpenAI",
        name: model,
        version
      }
    }
  };

  await httpsPut(url, token, body);
}

async function main() {
  const credential = new DefaultAzureCredential();
  const resourceClient = new ResourceManagementClient(credential, subscriptionId);
  const containerAppsClient = new ContainerAppsAPIClient(credential, subscriptionId);
  const acrClient = new ContainerRegistryManagementClient(credential, subscriptionId);
  const cognitiveClient = new CognitiveServicesManagementClient(credential, subscriptionId);
  const insightsClient = new ApplicationInsightsManagementClient(credential, subscriptionId);
  const workspaceClient = new OperationalInsightsManagementClient(credential, subscriptionId);

  const tags = { "azd-env-name": environmentName };
  const rgName = "rg-" + environmentName;
  const envName = "env-" + environmentName;
  const acrName = "acr" + environmentName.replace(/-/g, "").toLowerCase();
  const appName = "app-" + environmentName;
  const aoaiName = "aoai-" + environmentName;
  const workspaceName = "log-" + environmentName;
  const appInsightsName = "appi-" + environmentName;
  const identityName = "id-" + environmentName;
  const identityId = "/subscriptions/" + subscriptionId + "/resourceGroups/" + rgName +
    "/providers/Microsoft.ManagedIdentity/userAssignedIdentities/" + identityName;

  await resourceClient.resourceGroups.createOrUpdate(rgName, {
    location,
    tags
  });

  // Managed Identity
  await resourceClient.resources.beginCreateOrUpdateByIdAndWait(identityId, "2023-01-31", {
    location,
    tags
  });

  // Log Analytics Workspace
  const workspace = await workspaceClient.workspaces.beginCreateOrUpdateAndWait(rgName, workspaceName, {
    location,
    sku: { name: "PerGB2018" },
    retentionInDays: 30
  });

  // App Insights (classic mode linked to workspace)
  await insightsClient.components.createOrUpdate(rgName, appInsightsName, {
    kind: "web",
    applicationType: "web",
    location,
    workspaceResourceId: workspace.id
  } as any);

  // Container App Environment
  const sharedKeys = await workspaceClient.sharedKeysOperations.getSharedKeys(rgName, workspaceName);
  const env = await containerAppsClient.managedEnvironments.beginCreateOrUpdateAndWait(rgName, envName, {
    location,
    tags,
    appLogsConfiguration: {
      destination: "log-analytics",
      logAnalyticsConfiguration: {
        customerId: workspace.customerId!,
        sharedKey: sharedKeys.primarySharedKey!
      }
    }
  });

  await waitForManagedEnvReady(rgName, envName, containerAppsClient);

  const acr = await acrClient.registries.beginCreateAndWait(rgName, acrName, {
    location,
    sku: { name: "Basic" },
    adminUserEnabled: true
  });

  const acrCreds = await acrClient.registries.listCredentials(rgName, acrName);
  const acrUsername = acrCreds.username;
  const acrPassword = acrCreds.passwords?.[0]?.value;
  if (!acrPassword) throw new Error("ACR password not available");

  await containerAppsClient.containerApps.beginCreateOrUpdateAndWait(rgName, appName, {
    location,
    managedEnvironmentId: env.id,
    configuration: {
      ingress: { external: true, targetPort: 3000 },
      secrets: [
        { name: "registry-password", value: acrPassword }
      ],
      registries: [
        {
          server: acr.loginServer,
          username: acrUsername,
          passwordSecretRef: "registry-password"
        }
      ]
    },
    template: {
      containers: [{
        name: "app",
        image: "mcr.microsoft.com/azuredocs/containerapps-helloworld:latest",
        resources: { cpu: 0.5, memory: "1Gi" }
      }]
    },
    tags: {
      ...tags,
      "azd-service-name": serviceName
    }
  });

  await cognitiveClient.accounts.beginCreateAndWait(rgName, aoaiName, {
    location,
    kind: "OpenAI",
    sku: { name: "S0" },
    properties: {
      customSubDomainName: aoaiName,
      publicNetworkAccess: "Enabled"
    },
    tags
  });

  await deployModel(
    azdConfig.chat.deployment,
    azdConfig.chat.model,
    azdConfig.chat.version,
    azdConfig.chat.capacity
  );

  await deployModel(
    azdConfig.embedding.deployment,
    azdConfig.embedding.model,
    azdConfig.embedding.version,
    azdConfig.embedding.capacity
  );

  const escapedPrompt = azdConfig.system_prompt.replace(/"/g, '\\"').replace(/\n/g, '\\n').replace(/\r/g, '\\r');

  const outputs = {
    AZURE_CONTAINER_REGISTRY_ENDPOINT: { value: acr.loginServer },
    AZURE_RESOURCE_LLAMA_INDEX_JAVASCRIPT_ID: { value: "/subscriptions/" + subscriptionId + "/resourceGroups/rg-" + environmentName + "/providers/Microsoft.App/containerApps/" + azdConfig.serviceName },
    AZURE_OPENAI_ENDPOINT: { value: "https://" + aoaiName + ".openai.azure.com" },
    AZURE_DEPLOYMENT_NAME: { value: azdConfig.chat.deployment },
    AZURE_OPENAI_API_VERSION: { value: azdConfig.openai_api_version },
    MODEL_PROVIDER: { value: azdConfig.model_provider },
    MODEL: { value: azdConfig.chat.model },
    EMBEDDING_MODEL: { value: azdConfig.embedding.model },
    EMBEDDING_DIM: { value: azdConfig.embedding.dim },
    OPENAI_API_KEY: { value: azdConfig.openai_api_key },
    LLM_TEMPERATURE: { value: azdConfig.llm_temperature },
    LLM_MAX_TOKENS: { value: azdConfig.llm_max_tokens },
    TOP_K: { value: azdConfig.top_k },
    FILESERVER_URL_PREFIX: { value: azdConfig.fileserver_url_prefix },
    SYSTEM_PROMPT: { value: escapedPrompt },
    STORAGE_CACHE_DIR: { value: "./cache" }
  };

  fs.writeFileSync("outputs.json", JSON.stringify(outputs, null, 2));
  console.log(JSON.stringify(outputs, null, 2));

  for (const [key, val] of Object.entries(outputs)) {
    const value = val.value;
    if (value === null || value === undefined || value === "") continue;
    execSync("azd env set " + key + " \"" + value.replace(/"/g, '\\"') + "\"", { stdio: "inherit" });
  }
}

main().catch(err => {
  console.error(err);
  process.exit(1);
});

`

// PackageJsonTemplate contains the package.json for TypeScript infrastructure
const PackageJsonTemplate = `{
	"name": "infra",
	"version": "1.0.0",
	"main": "index.js",
	"scripts": {
		"build": "tsc -p tsconfig.build.json",
		"start": "node dist/deploy.js"
	},
	"author": "",
	"license": "ISC",
	"description": "",
	"dependencies": {
		"@azure/arm-resources": "latest",
		"@azure/arm-appcontainers": "latest",
		"@azure/arm-containerregistry": "latest",
		"@azure/arm-cognitiveservices": "latest",
		"@azure/arm-operationalinsights": "latest",
		"@azure/arm-appinsights": "latest",
		"@azure/identity": "latest",
		"typescript": "^5.4.2"
	},
	"devDependencies": {
		"@types/node": "^20.4.2"
	}
}`

// TsconfigJsonTemplate contains the tsconfig.json for TypeScript infrastructure
const TsconfigJsonTemplate = `{
	"compilerOptions": {
		"target": "ES2020",
		"module": "CommonJS",
		"strict": true,
		"esModuleInterop": true,
		"allowSyntheticDefaultImports": true,
		"skipLibCheck": true,
		"forceConsistentCasingInFileNames": true,
		"outDir": "dist",
		"paths": {
			"http": ["./node_modules/@types/node"],
			"https": ["./node_modules/@types/node"]
		}
	},
	"include": ["src/**/*.ts", "config/**/*.ts", "azd.config.json"],
	"exclude": ["node_modules"]
}`

// TsconfigBuildJsonTemplate contains the tsconfig.build.json for TypeScript infrastructure
const TsconfigBuildJsonTemplate = `{
	"extends": "./tsconfig.json",
	"compilerOptions": {
		"noEmitOnError": false,
		"skipLibCheck": true,
		"skipDefaultLibCheck": true
	},
	"include": ["src/**/*.ts", "config/**/*.ts", "azd.config.json"],
	"exclude": ["node_modules"]
}`

// Added azdConfig as a separate variable to ensure it is included in the template folder during the init phase.
const AzdConfigTemplate = `{
  "chat": {
    "model": "gpt-4o-mini",
    "deployment": "gpt-4o-mini",
    "version": "2024-07-18",
    "capacity": 10
  },
  "embedding": {
    "model": "text-embedding-3-large",
    "deployment": "text-embedding-3-large",
    "version": "1",
    "dim": "1024",
    "capacity": 10
  },
  "model_provider": "openai",
  "serviceName": "llama-index-javascript",
  "openai_api_key": "",
  "llm_temperature": "0.7",
  "llm_max_tokens": "100",
  "openai_api_version": "2024-02-15-preview",
  "top_k": "3",
  "fileserver_url_prefix": "http://localhost/api/files",
  "system_prompt": "You are a helpful assistant who helps users with their questions."
}`

// Add Dockerfile template
const DockerfileTemplate = `FROM node:20-alpine as build

WORKDIR /app

COPY package.json package-lock.* ./
RUN npm install

# Build the application
COPY . .
RUN npm run build

# ====================================
FROM build as release

EXPOSE 3000

CMD ["npm", "run", "start"]`

// DestroyTsTemplate contains the destroy.ts for TypeScript infrastructure
const DestroyTsTemplate = 
  "import { DefaultAzureCredential } from \"@azure/identity\";\n" +
  "import { ResourceManagementClient } from \"@azure/arm-resources\";\n\n" +
  "const subscriptionId = process.env.AZURE_SUBSCRIPTION_ID!;\n" +
  "const resourceGroupName = \"rg-\" + process.env.AZURE_ENV_NAME;\n\n" +
  "async function main() {\n" +
  "  const credential = new DefaultAzureCredential();\n" +
  "  const resourceClient = new ResourceManagementClient(credential, subscriptionId);\n\n" +
  "  console.error(\"[destroy] Deleting resource group: \" + resourceGroupName);\n\n" +
  "  let resourceCount = 0;\n" +
  "  for await (const _ of resourceClient.resources.listByResourceGroup(resourceGroupName)) {\n" +
  "    resourceCount++;\n" +
  "  }\n\n" +
  "  console.error(\"[destroy] Found \" + resourceCount + \" resources. Proceeding to delete...\");\n\n" +
  "  await resourceClient.resourceGroups.beginDeleteAndWait(resourceGroupName);\n\n" +
  "  console.error(\"[destroy] Resource group \" + resourceGroupName + \" deleted.\");\n" +
  "  console.log(JSON.stringify({ success: true }));\n" +
  "}\n\n" +
  "main().catch(err => {\n" +
  "  console.error(\"Error during resource deletion:\", err);\n" +
  "  process.exit(1);\n" +
  "});\n"

