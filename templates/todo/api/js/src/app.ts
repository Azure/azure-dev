import express, { Express } from "express";
import swaggerUI from "swagger-ui-express";
import cors from "cors";
import yaml from "yamljs";
import { getConfig } from "./config";
import lists from "./routes/lists";
import items from "./routes/items";
import { configureMongoose } from "./models/mongoose";
import { observability } from "./config/observability";

// For Azure services which don't support setting CORS directly within the service (like Azure Container Apps)
// You can enable localhost cors access here.
//    example: const localhostOrigin = "http://localhost:3000";
// Keep empty string to deny localhost origin.
const localhostOrigin = "";

export const createApp = async (): Promise<Express> => {
    const config = await getConfig();
    const app = express();

    // Configuration
    observability(config.observability);
    await configureMongoose(config.database);
    // Middleware
    app.use(express.json());

    // env.ENABLE_ORYX_BUILD is only set on Azure environment during azd provision for todo-templates
    // You can update this to env.NODE_ENV if your app is using `development` to run locally and another value
    // when the app is running on Azure (like production or stating)
    const runningOnAzure = process.env.ENABLE_ORYX_BUILD;

    if (runningOnAzure) {
        // REACT_APP_WEB_BASE_URL must be set for the api service as a property
        // otherwise the api server will reject the origin.
        const apiUrlSet = process.env.REACT_APP_WEB_BASE_URL;
        const originList = [
            "https://portal.azure.com",
            "https://ms.portal.azure.com",
        ];
        if (apiUrlSet) {
            originList.push(apiUrlSet);
        }
        if (localhostOrigin) {
            originList.push(localhostOrigin);
            console.log(`Allowing requests from ${localhostOrigin}. To change or disable, go to ${__filename}`);
        }

        app.use(cors({
            origin: originList
        }));
    }
    else {
        app.use(cors());
        console.log("Allowing requests from any origin because the server is running locally.");
    }

    // API Routes
    app.use("/lists/:listId/items", items);
    app.use("/lists", lists);

    // Swagger UI
    const swaggerDocument = yaml.load("./openapi.yaml");
    app.use("/", swaggerUI.serve, swaggerUI.setup(swaggerDocument));

    return app;
};
