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
	files[filepath.Join(infraDir, "deploy.ts")] = []byte(DeployTsTemplate)
	files[filepath.Join(infraDir, "package.json")] = []byte(PackageJsonTemplate)
	files[filepath.Join(infraDir, "tsconfig.json")] = []byte(TsconfigJsonTemplate)
	files[filepath.Join(infraDir, "tsconfig.build.json")] = []byte(TsconfigBuildJsonTemplate)

	return files
}

// DeployTsTemplate is the main TypeScript infrastructure template
const DeployTsTemplate = `import { DefaultAzureCredential } from "@azure/identity";
import { ResourceManagementClient } from "@azure/arm-resources";
import { ContainerAppsAPIClient } from "@azure/arm-appcontainers";
import { ContainerRegistryManagementClient } from "@azure/arm-containerregistry";
import { CognitiveServicesManagementClient } from "@azure/arm-cognitiveservices";
import * as fs from "fs";
import * as https from "https";

const subscriptionId = process.env.AZURE_SUBSCRIPTION_ID!;
const environmentName = process.env.AZURE_ENV_NAME!;
const location = process.env.AZURE_LOCATION!;
const principalId = process.env.AZURE_PRINCIPAL_ID!;

const llamaIndexConfig = {
  chat: {
    model: "gpt-4o-mini",
    deployment: "gpt-4o-mini",
    version: "2024-07-18",
    capacity: 10
  },
  embedding: {
    model: "text-embedding-3-large",
    deployment: "text-embedding-3-large",
    version: "1",
    dim: "1024",
    capacity: 10
  },
  model_provider: "openai",
  openai_api_key: "",
  llm_temperature: "0.7",
  llm_max_tokens: "100",
  openai_api_version: "2024-02-15-preview",
  top_k: "3",
  fileserver_url_prefix: "http://localhost/api/files",
  system_prompt: "You are a helpful assistant who helps users with their questions."
};

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

async function main() {
  const credential = new DefaultAzureCredential();
  const resourceClient = new ResourceManagementClient(credential as any, subscriptionId);
  const containerAppsClient = new ContainerAppsAPIClient(credential as any, subscriptionId);
  const acrClient = new ContainerRegistryManagementClient(credential as any, subscriptionId);
  const cognitiveClient = new CognitiveServicesManagementClient(credential as any, subscriptionId);

  const tags = { "azd-env-name": environmentName };
  const rgName = "rg-" + environmentName;
  const envName = "env-" + environmentName;
  const acrName = "acr" + environmentName.replace(/-/g, "").toLowerCase();
  const appName = "app-" + environmentName;
  const aoaiName = "aoai-" + environmentName;

  await resourceClient.resourceGroups.createOrUpdate(rgName, { location: location, tags: tags });

  const envParams = {
    location: location,
    tags: tags,
    properties: {
      zoneRedundant: false
    }
  };

  const env = await containerAppsClient.managedEnvironments.beginCreateOrUpdateAndWait(rgName, envName, envParams);

  await waitForManagedEnvReady(rgName, envName, containerAppsClient);

  const acr = await acrClient.registries.beginCreateAndWait(rgName, acrName, {
    location: location,
    sku: { name: "Basic" },
    adminUserEnabled: true
  });

  const acrCreds = await acrClient.registries.listCredentials(rgName, acrName);
  const acrUsername = acrCreds.username;
  const acrPassword = acrCreds.passwords?.[0]?.value;
  if (!acrPassword) throw new Error("ACR password not available");

  await containerAppsClient.containerApps.beginCreateOrUpdateAndWait(rgName, appName, {
    location: location,
    managedEnvironmentId: env.id,
    configuration: {
      ingress: { external: true, targetPort: 3000 },
      registries: [{
        server: acr.loginServer,
        username: acrUsername,
        passwordSecretRef: "registry-password"
      }]
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
      "azd-service-name": "app"
    }
  });

  await cognitiveClient.accounts.beginCreateAndWait(rgName, aoaiName, {
    location: location,
    kind: "OpenAI",
    sku: { name: "S0" },
    properties: {
      customSubDomainName: aoaiName,
      publicNetworkAccess: "Enabled"
    },
    tags: tags
  });

  const openAiEndpoint = "https://" + aoaiName + ".openai.azure.com";
  const token = (await credential.getToken("https://management.azure.com/.default"))?.token;
  if (!token) throw new Error("Failed to get token");

  async function deployModel(
    deployment: string,
    model: string,
    version: string,
    capacity: number
  ) {
    const url = "https://management.azure.com/subscriptions/" + subscriptionId +
                "/resourceGroups/" + rgName +
                "/providers/Microsoft.CognitiveServices/accounts/" + aoaiName +
                "/deployments/" + deployment +
                "?api-version=2023-10-01-preview";

    const body = {
      sku: { name: "Standard" },
      properties: {
        model: { format: "OpenAI", name: model, version: version },
        scaleSettings: { scaleType: "Manual", capacity: capacity }
      }
    };

    await httpsPut(url, token, body);
  }

  await deployModel(
    llamaIndexConfig.chat.deployment,
    llamaIndexConfig.chat.model,
    llamaIndexConfig.chat.version,
    llamaIndexConfig.chat.capacity
  );

  await deployModel(
    llamaIndexConfig.embedding.deployment,
    llamaIndexConfig.embedding.model,
    llamaIndexConfig.embedding.version,
    llamaIndexConfig.embedding.capacity
  );

  const outputs = {
    AZURE_CONTAINER_REGISTRY_ENDPOINT: { value: acr.loginServer },
    AZURE_RESOURCE_LLAMA_INDEX_JAVASCRIPT_ID: { value: "" },
    AZURE_OPENAI_ENDPOINT: { value: openAiEndpoint },
    AZURE_DEPLOYMENT_NAME: { value: llamaIndexConfig.chat.deployment },
    AZURE_OPENAI_API_VERSION: { value: llamaIndexConfig.openai_api_version },
    MODEL_PROVIDER: { value: llamaIndexConfig.model_provider },
    MODEL: { value: llamaIndexConfig.chat.model },
    EMBEDDING_MODEL: { value: llamaIndexConfig.embedding.model },
    EMBEDDING_DIM: { value: llamaIndexConfig.embedding.dim },
    OPENAI_API_KEY: { value: llamaIndexConfig.openai_api_key },
    LLM_TEMPERATURE: { value: llamaIndexConfig.llm_temperature },
    LLM_MAX_TOKENS: { value: llamaIndexConfig.llm_max_tokens },
    TOP_K: { value: llamaIndexConfig.top_k },
    FILESERVER_URL_PREFIX: { value: llamaIndexConfig.fileserver_url_prefix },
    SYSTEM_PROMPT: { value: llamaIndexConfig.system_prompt },
    OPENAI_API_TYPE: { value: "AzureOpenAI" },
    STORAGE_CACHE_DIR: { value: "./cache" }
  };

  fs.writeFileSync("outputs.json", JSON.stringify(outputs, null, 2));
  console.log(JSON.stringify(outputs, null, 2));
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
	"include": ["deploy.ts"],
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
	"include": ["deploy.ts"],
	"exclude": ["node_modules"]
}`
