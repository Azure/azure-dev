using System.CommandLine;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Azd; // Generated gRPC namespace

class Program
{
    public static async Task<int> Main(string[] args)
    {
        var services = new ServiceCollection();

        services.AddSingleton<AzdClient>();

        // Register commands
        services.AddTransient<ContextCommand>();
        services.AddTransient<PromptCommand>();
        services.AddTransient<ListenCommand>();

        var serviceProvider = services.BuildServiceProvider();
        var rootCommand = new RootCommand("azd CLI tool");

        // Add commands from DI
        rootCommand.AddCommand(serviceProvider.GetRequiredService<ContextCommand>().Build());
        rootCommand.AddCommand(serviceProvider.GetRequiredService<PromptCommand>().Build());
        rootCommand.AddCommand(serviceProvider.GetRequiredService<ListenCommand>().Build());

        // ✅ Parse & execute
        return await rootCommand.InvokeAsync(args);
    }
}
