using Azure.Identity;
using Microsoft.ApplicationInsights.AspNetCore.Extensions;
using MongoDB.Driver;
using SimpleTodo.Api;

var builder = WebApplication.CreateBuilder(args);
builder.Configuration.AddAzureKeyVault(new Uri(builder.Configuration["AZURE_KEY_VAULT_ENDPOINT"]), new DefaultAzureCredential());

builder.Services.AddSingleton<ListsRepository>();
builder.Services.AddSingleton(_ => new MongoClient(builder.Configuration[builder.Configuration["AZURE_COSMOS_CONNECTION_STRING_KEY"]]));
builder.Services.AddControllers();

var options = new ApplicationInsightsServiceOptions { ConnectionString = builder.Configuration["APPLICATIONINSIGHTS_CONNECTION_STRING"] };
builder.Services.AddApplicationInsightsTelemetry(options);

var app = builder.Build();

app.UseCors(policy =>
{
    policy.AllowAnyOrigin();
    policy.AllowAnyHeader();
    policy.AllowAnyMethod();
});

app.MapControllers();
app.Run();