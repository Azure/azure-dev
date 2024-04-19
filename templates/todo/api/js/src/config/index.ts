import { AppConfig, DatabaseConfig, ObservabilityConfig } from "./appConfig";
import dotenv from "dotenv";
import { DefaultAzureCredential } from "@azure/identity";
import { SecretClient } from "@azure/keyvault-secrets";
import { load } from "@azure/app-configuration-provider";
import { logger } from "../config/observability";
import { IConfig } from "config";

export const getConfig: () => Promise<AppConfig> = async () => {
    logger.info(`Loading configuration in ${process.env.NODE_ENV} environment...`);
    // For local dev/debug, prepare AZURE_COSMOS_CONNECTION_STRING, AZURE_APPINSIGHTS_CONNECTIONSTRING in src/api/.env file
    if (process.env.NODE_ENV?.toLowerCase() != "production") {
        logger.info("Loading ENV variables from .env file...");
        dotenv.config();
    }
    
    // Load ENV variables from Azure App Configuration when using binding
    if (process.env.AZURE_APPCONFIGURATION_ENDPOINT) {
        logger.info("Loading ENV variables from Azure App Configuration...");
        await populateEnvironmentFromAppConfig();
    }
    // Load ENV variables from Azure KeyVault by default
    else {
        logger.info("Loading ENV variables from Azure KeyVault...");
        await populateEnvironmentFromKeyVault();
    }

    // Load configuration after population into environment variables is complete
    // eslint-disable-next-line @typescript-eslint/no-var-requires
    const config: IConfig = require("config") as IConfig;
    const databaseConfig = config.get<DatabaseConfig>("database");
    const observabilityConfig = config.get<ObservabilityConfig>("observability");

    if (!databaseConfig.connectionString) {
        logger.warn("database.connectionString is required but has not been set. Ensure environment variable 'AZURE_COSMOS_CONNECTION_STRING' has been set");
    }

    if (!observabilityConfig.connectionString) {
        logger.warn("observability.connectionString is required but has not been set. Ensure environment variable 'APPLICATIONINSIGHTS_CONNECTION_STRING' has been set");
    }

    return {
        observability: {
            connectionString: observabilityConfig.connectionString,
            roleName: observabilityConfig.roleName,
        },
        database: {
            connectionString: databaseConfig.connectionString,
            databaseName: databaseConfig.databaseName,
        },
    };
};

const populateEnvironmentFromKeyVault = async () => {
    // If Azure key vault endpoint is defined
    // 1. Login with Default credential (managed identity or service principal)
    // 2. Overlay key vault secrets on top of ENV vars
    const keyVaultEndpoint = process.env.AZURE_KEY_VAULT_ENDPOINT || "";

    if (!keyVaultEndpoint) {
        logger.warn("AZURE_KEY_VAULT_ENDPOINT has not been set. Configuration will be loaded from current environment.");
        return;
    }

    try {
        logger.info("Populating environment from Azure KeyVault...");
        const credential = new DefaultAzureCredential({});
        const secretClient = new SecretClient(keyVaultEndpoint, credential);

        for await (const secretProperties of secretClient.listPropertiesOfSecrets()) {
            const secret = await secretClient.getSecret(secretProperties.name);

            // KeyVault does not support underscores in key names and replaces '-' with '_'
            // Expect KeyVault secret names to be in conventional capitalized snake casing after conversion
            const keyName = secret.name.replace(/-/g, "_");
            process.env[keyName] = secret.value;
        }
    }
    catch (err: any) {
        logger.error(`Error authenticating with Azure KeyVault.  Ensure your managed identity or service principal has GET/LIST permissions. Error: ${err}`);
        throw err;
    }
};

const populateEnvironmentFromAppConfig = async () => {
    // If Azure AppConfig endpoint is defined
    // 1. Login with Default credential (managed identity or service principal)
    // 2. Overlay App Config configurations on top of ENV vars (Read from KeyVault secret if is KeyVault references)
    const endpoint = process.env.AZURE_APPCONFIGURATION_ENDPOINT || "";

    if (!endpoint) {
        logger.warn("AZURE_APPCONFIGURATION_ENDPOINT has not been set. Configuration will be loaded from current environment.");
        return;
    }

    try {
        logger.info("Populating environment from Azure AppConfiguration...");
        const credential = new DefaultAzureCredential();

        const settings = await load(endpoint, credential, {
            keyVaultOptions: {
                // Access keyvault using the same identity, make sure the permission is set correctly.
                credential: credential
            }
        });

        for (const [key, value] of settings) {
            process.env[key] = value;
        }
    }
    catch (err: any) {
        logger.error(`Error reading configs from Azure App Configuration or Keyvault. Ensure your managed identity or service principal has GET/LIST permissions. Error: ${err}`);
        throw err;
    }
};