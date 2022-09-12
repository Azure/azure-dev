# Java with Spring Boot REST API

## Setup

### Prerequisites

- Java 17 or later

### Local Environment

Create a `.env` with the following configuration:

- `AZURE_COSMOS_CONNECTION_STRING` - Cosmos DB connection string (Mongo DB also supported)

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
