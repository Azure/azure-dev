using System.CommandLine;
using Spectre.Console;
using Microsoft.Azd;

public class ContextCommand
{
    private readonly AzdClient _azdClient;

    public ContextCommand(AzdClient azdClient)
    {
        _azdClient = azdClient;
    }

    public Command Build()
    {
        var command = new Command("context", "Get the context of the AZD project & environment.");

        command.SetHandler(async () =>
        {
            try
            {
                // === User Config ===
                var configResponse = await _azdClient.UserConfig.GetAsync(new());
                if (configResponse?.Found == true)
                {
                    AnsiConsole.MarkupLine("[white]User Config[/]");

                    var config = System.Text.Json.JsonSerializer.Deserialize<Dictionary<string, string>>(configResponse.Value.ToStringUtf8());
                    if (config is not null)
                    {
                        AnsiConsole.WriteLine(System.Text.Json.JsonSerializer.Serialize(config, new System.Text.Json.JsonSerializerOptions
                        {
                            WriteIndented = true
                        }));
                        AnsiConsole.WriteLine();
                    }
                }

                // === Project ===
                var projectResponse = await _azdClient.Project.GetAsync(new());
                if (projectResponse is not null)
                {
                    AnsiConsole.MarkupLine("[cyan]Project:[/]");
                    AnsiConsole.MarkupLine($"{MarkupKey("Name")}: {projectResponse.Project.Name}");
                    AnsiConsole.MarkupLine($"{MarkupKey("Path")}: {projectResponse.Project.Path}");
                    AnsiConsole.WriteLine();
                }
                else
                {
                    AnsiConsole.MarkupLine("[yellow]WARNING:[/] No AZD project found in current directory.");
                    AnsiConsole.MarkupLine($"Run [cyan]azd init[/] to create a new project.");
                    return;
                }

                // === Environment ===
                var currentEnv = await _azdClient.Environment.GetCurrentAsync(new());
                if (currentEnv == null)
                {
                    AnsiConsole.MarkupLine("[yellow]WARNING:[/] No AZD environment(s) found.");
                    AnsiConsole.MarkupLine($"Run [cyan]azd env new[/] to create one.");
                    return;
                }

                var currentEnvName = currentEnv.Environment.Name;
                var envList = await _azdClient.Environment.ListAsync(new());

                if (envList.Environments.Count == 0)
                {
                    AnsiConsole.MarkupLine("No environments found.");
                }
                else
                {
                    AnsiConsole.MarkupLine("[cyan]Environments:[/]");
                    foreach (var env in envList.Environments)
                    {
                        var selected = env.Name == currentEnvName ? " [white](selected)[/]" : "";
                        AnsiConsole.MarkupLine($"- {env.Name}{selected}");
                    }
                    AnsiConsole.WriteLine();
                }

                // === Environment values ===
                var envValues = await _azdClient.Environment.GetValuesAsync(new() { Name = currentEnvName });
                if (envValues is not null)
                {
                    AnsiConsole.MarkupLine("[cyan]Environment values:[/]");
                    foreach (var kv in envValues.KeyValues)
                    {
                        AnsiConsole.MarkupLine($"{MarkupKey(kv.Key)}: {MarkupVal(kv.Value)}");
                    }
                    AnsiConsole.WriteLine();
                }

                // === Deployment Context ===
                var deploymentCtx = await _azdClient.Deployment.GetDeploymentContextAsync(new());
                if (deploymentCtx is not null)
                {
                    var scope = deploymentCtx.AzureContext.Scope;
                    var scopeMap = new Dictionary<string, string?>
                    {
                        ["Tenant ID"] = scope.TenantId,
                        ["Subscription ID"] = scope.SubscriptionId,
                        ["Location"] = scope.Location,
                        ["Resource Group"] = scope.ResourceGroup
                    };

                    AnsiConsole.MarkupLine("[cyan]Deployment Context:[/]");
                    foreach (var kv in scopeMap)
                    {
                        AnsiConsole.MarkupLine($"{MarkupKey(kv.Key)}: {kv.Value ?? "N/A"}");
                    }
                    AnsiConsole.WriteLine();

                    AnsiConsole.MarkupLine("[cyan]Provisioned Azure Resources:[/]");
                    foreach (var id in deploymentCtx.AzureContext.Resources)
                    {
                        if (AzureResource.TryParse(id, out var parsed))
                        {
                            AnsiConsole.MarkupLine($"- {parsed.Name} ({MarkupVal(parsed.Type)})");
                        }
                        else
                        {
                            AnsiConsole.MarkupLine($"- {id} [grey](unparsed)[/]");
                        }
                    }
                    AnsiConsole.WriteLine();
                }
            }
            catch (Exception ex)
            {
                AnsiConsole.MarkupLine($"[red]ERROR:[/] {ex.Message}");
            }
        });

        return command;
    }

    private string MarkupKey(string value) => $"[white]{value}[/]";
    private string MarkupVal(string value) => $"[grey]{value}[/]";

    // Optional: Resource parser stub
    private record AzureResource(string Name, string Type)
    {
        public static bool TryParse(string resourceId, out AzureResource resource)
        {
            var parts = resourceId.Split('/');
            var name = parts.LastOrDefault();
            var type = parts.Length >= 3 ? $"{parts[^3]}/{parts[^2]}" : "unknown";

            resource = new AzureResource(name ?? "unknown", type);
            return !string.IsNullOrEmpty(name);
        }
    }
}
