var builder = DistributedApplication.CreateBuilder(args);

// Use Aspire 9.4 to own the Azure Container App environment
builder.AddAzureContainerAppEnvironment("appHostInfrastructure");

// test param with default value
var goVersion = builder.AddParameter("goversion", "1.22", publishValueAsDefault: true);

// the back-end API the front end will call
var apiservice = builder.AddProject<Projects.AspireAzdTests_ApiService>("apiservice");

// worker with no bindings
var workerProj = builder.AddProject<Projects.AspireAzdTests_Worker>("worker");

// the front end app
_ = builder.AddProject<Projects.AspireAzdTests_Web>("webfrontend")
    .WithExternalHttpEndpoints()
    .WithReference(apiservice)
    .WithReference(workerProj)
    .WithEnvironment("GOVERSION", goVersion);

builder.Build().Run();
