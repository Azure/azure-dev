using Azure.Identity;
using Microsoft.ApplicationInsights.AspNetCore.Extensions;
using MongoDB.Driver;
using SimpleTodo.Api;

var builder = WebApplication.CreateBuilder(args);
var credential = new ChainedTokenCredential(new AzureDeveloperCliCredential(), new DefaultAzureCredential());
builder.Configuration.AddAzureKeyVault(new Uri(builder.Configuration["AZURE_KEY_VAULT_ENDPOINT"]), credential);

builder.Services.AddSingleton<ListsRepository>();
builder.Services.AddSingleton(_ => new MongoClient(builder.Configuration[builder.Configuration["AZURE_COSMOS_CONNECTION_STRING_KEY"]]));
builder.Services.AddControllers();
builder.Services.AddApplicationInsightsTelemetry(builder.Configuration);

var app = builder.Build();

app.UseCors(policy =>
{
    policy.AllowAnyOrigin();
    policy.AllowAnyHeader();
    policy.AllowAnyMethod();
});

// Swagger UI
app.UseSwaggerUI(options => {
    options.SwaggerEndpoint("./openapi.yaml", "v1");
    options.RoutePrefix = "";
});

app.UseStaticFiles(new StaticFileOptions{
    // Serve openapi.yaml file
    ServeUnknownFileTypes = true,
});

app.MapControllers();
app.Run();