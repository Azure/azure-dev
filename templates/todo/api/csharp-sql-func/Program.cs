using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Azure.Identity;
using Microsoft.EntityFrameworkCore;
using Microsoft.Extensions.Configuration;

namespace SimpleTodo.Api;
class Program
{
    static async Task Main(string[] args)
    {
        var credential = new DefaultAzureCredential();
        var host = new HostBuilder()
            .ConfigureFunctionsWorkerDefaults()
            .ConfigureAppConfiguration(config => 
                config.AddAzureKeyVault(new Uri(Environment.GetEnvironmentVariable("AZURE_KEY_VAULT_ENDPOINT")!), credential))
            .ConfigureServices((config, services) =>
            {
                services.AddScoped<ListsRepository>();
                services.AddDbContext<TodoDb>(options =>
                {
                    var connectionString = config.Configuration[config.Configuration["AZURE_SQL_CONNECTION_STRING_KEY"]];
                    options.UseSqlServer(connectionString, sqlOptions =>
                    sqlOptions.EnableRetryOnFailure());
                });
            })
        .Build();
        
        await using (var scope = host.Services.CreateAsyncScope())
        {
            var db = scope.ServiceProvider.GetRequiredService<TodoDb>();
            await db.Database.EnsureCreatedAsync();
        }
        await host.RunAsync();
    }
}