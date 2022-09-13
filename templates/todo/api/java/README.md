# Java with Spring Boot REST API

## Setup

### Prerequisites

- Java 17 or later

### Local Environment

Create an `.env` file.
Set the property values by either providing a KeyVault endpoint, or to provide the secrets directly.

#### Option 1: Use KeyVault to provide secrets

Set `AZURE_KEY_VAULT_ENDPOINT`. With this set, other secret environment properties such as `AZURE_COSMOS_CONNECTION_STRING` are loaded from KeyVault.

#### Option 2: Provide secrets directly

Set `AZURE_COSMOS_CONNECTION_STRING`, the Cosmos DB connection string (Mongo DB also supported)

#### Local ApplicationInsights telemetry

To configure Application Insights locally, either

- Set an environment variable `APPLICATIONINSIGHTS_CONNECTION_STRING`
- Update the `connectionString` defined in `applicationinsights.json`

### Build & Compile

Run `./mvnw package` to build & compile the application in the `target` directory.

### Run the application locally

Run `./mvnw spring-boot:run` to start the local development server.

The REST API will be available at `http://localhost:8080`.

### Build the Docker image

Run `./mvnw spring-boot:build-image` to build the Docker image.

### Run the Docker image

The environment variable `AZURE_COSMOS_CONNECTION_STRING` must point to the Cosmos DB connection string.

Run `docker run -it -p 8080:8080 -e AZURE_COSMOS_CONNECTION_STRING=$AZURE_COSMOS_CONNECTION_STRING azure/azure-dev-todo-java` to start the Docker image.

## Deploy to Azure App Service using Maven

The Maven property `basename` must point to the base name of your project.

Run `./mvnw package azure-webapp:deploy -Dbasename=my-java-project` (and replace `my-java-project` by the base name of your project).
