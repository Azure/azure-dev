# Java with Spring Boot REST API

## Setup

### Prerequisites

- Java 17 or later

### Local Environment

#### Environment variables

The following environment variables are available for configuration:

- `AZURE_KEY_VAULT_ENDPOINT`. If set, other secret environment properties such as `AZURE_COSMOS_CONNECTION_STRING` are loaded from KeyVault.
- `AZURE_COSMOS_CONNECTION_STRING`. A direct override for specifying the Cosmos DB connection string (Mongo DB also supported).
- `APPLICATIONINSIGHTS_CONNECTION_STRING`. (Optional) Connection string of an Application Insights instance for telemetry to be logged.

### Build & Compile

Run `./mvnw package` to build & compile the application in the `target` directory.
`./mvnw package -DskipTests` may be used instead to skip start-up tests that will require app configuration defined.

### Run the application locally

Run `./mvnw spring-boot:run` to start the local development server.

The REST API will be available at `http://localhost:3100`.

### Build and run the Docker image

```bash
docker build . -t java-todo@latest
docker run -e AZURE_COSMOS_CONNECTION_STRING=$AZURE_COSMOS_CONNECTION_STRING -p 3100:3100 -t java-todo@latest
```

### Regenerate API from OpenAPI spec

Run `./mvnw -P openapigen compile` to regenerate the API model and interfaces from the OpenAPI spec.
