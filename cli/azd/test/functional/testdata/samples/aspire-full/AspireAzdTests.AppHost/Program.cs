var builder = DistributedApplication.CreateBuilder(args);

// redis instance the app will use for output caching
var redisCache      = builder.AddRedis("cache");

// redis instance the app will use for simple messages
var redisPubSub     = builder.AddRedis("pubsub");

// azure storage account the app will use for blob & table storage
var azureStorage    = builder
                        .AddAzureStorage("storage")
                            .UseEmulator();

// azure table storage for storing request data
var requestTable    = azureStorage.AddTables("requestlog");

// azure blob storage for storing markdown files
var markdownBlobs   = azureStorage.AddBlobs("markdown");

// azure queues for sending messages
var messageQueue    = azureStorage.AddQueues("messages");

// the back-end API the front end will call
var apiservice      = builder.AddProject<Projects.AspireAzdTests_ApiService>("apiservice");

// the front end app
_                   = builder
                        .AddProject<Projects.AspireAzdTests_Web>("webfrontend")
                           .WithReference(redisCache)
                           .WithReference(redisPubSub)
                           .WithReference(apiservice)
                           .WithReference(requestTable)
                           .WithReference(markdownBlobs)
                           .WithReference(messageQueue);

builder.Build().Run();
