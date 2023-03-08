import express, { Express } from "express";
import swaggerUI from "swagger-ui-express";
import cors from "cors";
import yaml from "yamljs";
import { getConfig } from "./config";
import lists from "./routes/lists";
import items from "./routes/items";
import { configureMongoose } from "./models/mongoose";
import { observability } from "./config/observability";

export const createApp = async (): Promise<Express> => {
    const config = await getConfig();
    const app = express();

    // Configuration
    observability(config.observability);
    await configureMongoose(config.database);
    // Middleware
    app.use(express.json());

    var apiUrl = process.env.REACT_APP_WEB_BASE_URL;
    if (apiUrl != ""){
        app.use(cors({
            origin: ["https://portal.azure.com",
            "https://ms.portal.azure.com",
            apiUrl]
        }));
    }
    else{
        app.use(cors());
        console.log("error: could get environment variable REACT_APP_WEB_BASE_URL");
    }

    // API Routes
    app.use("/lists/:listId/items", items);
    app.use("/lists", lists);

    // Swagger UI
    const swaggerDocument = yaml.load("./openapi.yaml");
    app.use("/", swaggerUI.serve, swaggerUI.setup(swaggerDocument));

    return app;
};
