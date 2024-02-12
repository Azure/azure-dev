var builder = DistributedApplication.CreateBuilder(args);

var apiService = builder.AddProject<Projects.AspireAzdTests_ApiService>("apiservice");

builder.AddProject<Projects.AspireAzdTests_Web>("webfrontend")
    .WithReference(apiService);

builder.Build().Run();
