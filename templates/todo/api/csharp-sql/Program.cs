using Microsoft.ApplicationInsights.AspNetCore.Extensions;
using Microsoft.EntityFrameworkCore;
using SimpleTodo.Api;

var builder = WebApplication.CreateBuilder(args);

builder.Services.AddScoped<ListsRepository>();
builder.Services.AddDbContext<TodoDb>(options =>
{
    options.UseSqlServer(builder.Configuration["AZURE_SQL_CONNECTION_STRING"], sqlOptions => sqlOptions.EnableRetryOnFailure());
});

builder.Services.AddControllers();
var options = new ApplicationInsightsServiceOptions { ConnectionString = builder.Configuration["APPLICATIONINSIGHTS_CONNECTION_STRING"] };
builder.Services.AddApplicationInsightsTelemetry(options);

var app = builder.Build();

await using (var scope = app.Services.CreateAsyncScope())
{
    var db = scope.ServiceProvider.GetRequiredService<TodoDb>();
    await db.Database.EnsureCreatedAsync();
}

app.UseCors(policy =>
{
    policy.AllowAnyOrigin();
    policy.AllowAnyHeader();
    policy.AllowAnyMethod();
});

app.MapControllers();
app.Run();