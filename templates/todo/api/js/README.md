# Node with Typescript Express REST API

## Setup

### Prerequisites

- Node (16.13.0)
- NPM (8.1.0)

### Local Environment

Create a `.env` with the following configuration:

- `AZURE_COSMOS_CONNECTION_STRING` - Cosmos DB connection string (Mongo DB also supported)
- `AZURE_COSMOS_DATABASE_NAME` - Cosmos DB database name (Will automatically be created if it doesn't exist) (default: Todo)
- `APPLICATIONINSIGHTS_CONNECTION_STRING` - Azure Application Insights connection string
- `APPLICATIONINSIGHTS_ROLE_NAME` - Azure Application Insights Role name (default: API)

### Install Dependencies

Run `npm ci` to install local dependencies

### Build & Compile

Run `npm run build` to build & compile the Typescript code into the `./dist` folder

### Run application

Run `npm start` to start the local development server

Launch browser @ `http://localhost:3100`. The default page hosts the Open API UI where you can try out the API
