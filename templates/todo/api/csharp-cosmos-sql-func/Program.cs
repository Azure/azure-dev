using Microsoft.Azure.Cosmos;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using SimpleTodo.Api;
using Azure.Identity;
using System.Threading.Tasks;
using System;

namespace SimpleTodo.Api;
class Program
{
    static async Task Main(string[] args)
    {

        var credential = new DefaultAzureCredential();
        var host = new HostBuilder()
                        .ConfigureFunctionsWorkerDefaults()
                        .ConfigureServices(services =>
                        {
                            services.AddSingleton(sp =>
                            {

                                return new CosmosClient(Environment.GetEnvironmentVariable("AZURE_COSMOS_ENDPOINT"), credential, new CosmosClientOptions
                                {
                                    SerializerOptions = new CosmosSerializationOptions
                                    {
                                        PropertyNamingPolicy = CosmosPropertyNamingPolicy.CamelCase
                                    }
                                });
                            }

                            ); services.AddSingleton<ListsRepository>();
                        })
                        .Build();

        await host.RunAsync();

    }
}