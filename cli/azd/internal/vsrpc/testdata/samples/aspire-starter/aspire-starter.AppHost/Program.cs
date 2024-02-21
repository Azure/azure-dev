var builder = DistributedApplication.CreateBuilder(args);

var apiService = builder.AddProject<Projects.aspire_starter_ApiService>("apiservice");

builder.AddProject<Projects.aspire_starter_Web>("webfrontend")
    .WithReference(apiService);

builder.Build().Run();
