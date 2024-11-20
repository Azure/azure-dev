using Aspire.Hosting.Azure;

var builder = DistributedApplication.CreateBuilder(args);

// test param with default value
var goVersion = builder.AddParameter("goversion", "1.22", publishValueAsDefault: true);

// redis instance the app will use for simple messages
var redisPubSub = builder.AddRedis("pubsub");

// azure storage account the app will use for blob & table storage
var azureStorage    = builder
                        .AddAzureStorage("storage")
                            .RunAsEmulator();

// azure table storage for storing request data
var requestTable    = azureStorage.AddTables("requestlog");

// azure blob storage for storing markdown files
var markdownBlobs   = azureStorage.AddBlobs("markdown");

// azure queues for sending messages
var messageQueue    = azureStorage.AddQueues("messages");

// the back-end API the front end will call
var apiservice = builder.AddProject<Projects.AspireAzdTests_ApiService>("apiservice");

var cosmos = builder.AddAzureCosmosDB("cosmos");
var cosmosDb = cosmos.AddDatabase("db3");

// worker with no bindings
var workerProj = builder.AddProject<Projects.AspireAzdTests_Worker>("worker");

// the front end app
_ = builder
                        .AddProject<Projects.AspireAzdTests_Web>("webfrontend")
                            .WithExternalHttpEndpoints()
                            .WithReference(redisPubSub)
                            .WithReference(requestTable)
                            .WithReference(markdownBlobs)
                            .WithReference(messageQueue)
                            .WithReference(apiservice)
                            .WithReference(cosmosDb)
                            .WithReference(workerProj)
                            .WithEnvironment("GOVERSION", goVersion);

builder.Build().Run();
