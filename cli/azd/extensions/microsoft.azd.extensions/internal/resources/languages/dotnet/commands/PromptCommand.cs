using System.CommandLine;
using Azure.Core;
using Azure.Identity;
using Azure.ResourceManager;
using Microsoft.Azd;
using Spectre.Console;

public class PromptCommand
{
    private readonly AzdClient _azdClient;

    public PromptCommand(AzdClient azd)
    {
        _azdClient = azd;
    }

    public Command Build()
    {
        var cmd = new Command("prompt", "Examples of prompting the user for input.");

        cmd.SetHandler(async () =>
        {
            var context = new AzureContext { Scope = new AzureScope() };

            // === Prompt user for service choices ===
            var multiSelectRequest = new MultiSelectRequest
            {
                Options = new MultiSelectOptions
                {
                    Message = "Which Azure services do you use most with AZD?"
                }
            };
            multiSelectRequest.Options.Choices.AddRange(
            [
                new MultiSelectChoice { Label = "Container Apps", Value = "container-apps" },
                new MultiSelectChoice { Label = "Functions", Value = "functions" },
                new MultiSelectChoice { Label = "Static Web Apps", Value = "static-web-apps" },
                new MultiSelectChoice { Label = "App Service", Value = "app-service" },
                new MultiSelectChoice { Label = "Cosmos DB", Value = "cosmos-db" },
                new MultiSelectChoice { Label = "SQL Database", Value = "sql-db" },
                new MultiSelectChoice { Label = "Storage", Value = "storage" },
                new MultiSelectChoice { Label = "Key Vault", Value = "key-vault" },
                new MultiSelectChoice { Label = "Kubernetes Service", Value = "kubernetes-service" },
            ]);

            await _azdClient.Prompt.MultiSelectAsync(multiSelectRequest);

            // === Confirm whether to continue with resource selection ===
            var confirmSearch = await _azdClient.Prompt.ConfirmAsync(new ConfirmRequest
            {
                Options = new ConfirmOptions
                {
                    Message = "Do you want to search for Azure resources?",
                    DefaultValue = true
                }
            });

            if (confirmSearch?.Value != true) return;

            // === Prompt subscription ===
            var subResponse = await _azdClient.Prompt.PromptSubscriptionAsync(new());
            context.Scope.SubscriptionId = subResponse.Subscription.Id;
            context.Scope.TenantId = subResponse.Subscription.TenantId;

            // === Setup Azure credentials and clients ===
            var credential = new AzureDeveloperCliCredential(new AzureDeveloperCliCredentialOptions
            {
                TenantId = context.Scope.TenantId
            });

            var armClient = new ArmClient(credential, context.Scope.SubscriptionId);
            var subscription = armClient.GetSubscriptionResource(new ResourceIdentifier($"/subscriptions/{context.Scope.SubscriptionId}"));

            string? fullResourceType = null;

            // === Ask to filter by resource type ===
            var filterByType = await _azdClient.Prompt.ConfirmAsync(new ConfirmRequest
            {
                Options = new ConfirmOptions
                {
                    Message = "Do you want to filter by resource type?",
                    DefaultValue = false
                }
            });

            if (filterByType?.Value == true)
            {
                var providers = subscription.GetResourceProviders().ToList();

                var registeredProviders = providers
                    .Where(p => p.Data.RegistrationState == "Registered")
                    .ToList();

                var selectOptionsForProvider = new SelectOptions
                {
                    Message = "Select a resource provider"
                };
                selectOptionsForProvider.Choices.AddRange(
                    registeredProviders.Select((p, i) => new SelectChoice { Label = p.Data.Namespace, Value = i.ToString() })
                );

                var selectedProviderResponse = await _azdClient.Prompt.SelectAsync(new SelectRequest
                {
                    Options = selectOptionsForProvider
                });

                var selectedProviderIndex = selectedProviderResponse.Value;
                var selectedProvider = registeredProviders[selectedProviderIndex];

                var resourceTypes = selectedProvider.Data.ResourceTypes;

                var selectOptionsForResourceType = new SelectOptions
                {
                    Message = $"Select a {selectedProvider.Data.Namespace} resource type"
                };
                selectOptionsForResourceType.Choices.AddRange(
                    resourceTypes.Select((rt, i) => new SelectChoice { Label = rt.ResourceType, Value = i.ToString() })
                );

                var selectedResourceTypeResponse = await _azdClient.Prompt.SelectAsync(new SelectRequest
                {
                    Options = selectOptionsForResourceType
                });
                var selectedResourceTypeIndex = selectedResourceTypeResponse.Value;

                var selectedType = resourceTypes[selectedResourceTypeIndex];
                fullResourceType = $"{selectedProvider.Data.Namespace}/{selectedType.ResourceType}";
            }

            // === Ask to filter by resource group ===
            var filterByGroup = await _azdClient.Prompt.ConfirmAsync(new ConfirmRequest
            {
                Options = new ConfirmOptions
                {
                    Message = "Do you want to filter by resource group?",
                    DefaultValue = false
                }
            });

            ResourceExtended? selectedResource;

            if (filterByGroup?.Value == true)
            {
                var rgResponse = await _azdClient.Prompt.PromptResourceGroupAsync(new PromptResourceGroupRequest
                {
                    AzureContext = context
                });

                context.Scope.ResourceGroup = rgResponse.ResourceGroup.Name;

                var resourceGroupRes = await _azdClient.Prompt.PromptResourceGroupResourceAsync(new PromptResourceGroupResourceRequest
                {
                    AzureContext = context,
                    Options = new PromptResourceOptions
                    {
                        ResourceType = fullResourceType,
                        SelectOptions = new PromptResourceSelectOptions
                        {
                            AllowNewResource = false
                        }
                    }
                });

                selectedResource = resourceGroupRes.Resource;
            }
            else
            {
                var subscriptionRes = await _azdClient.Prompt.PromptSubscriptionResourceAsync(new PromptSubscriptionResourceRequest
                {
                    AzureContext = context,
                    Options = new PromptResourceOptions
                    {
                        ResourceType = fullResourceType,
                        SelectOptions = new PromptResourceSelectOptions
                        {
                            AllowNewResource = false
                        }
                    }
                });

                selectedResource = subscriptionRes.Resource;
            }

            // === Parse and display resource info ===
            var parsed = new
            {
                SubscriptionID = selectedResource.Id.Split('/')[2],
                ResourceGroup = selectedResource.Id.Split('/')[4],
                Name = selectedResource.Id.Split('/').Last(),
                Location = selectedResource.Location
            };

            AnsiConsole.WriteLine();
            AnsiConsole.MarkupLine("[cyan]Selected resource:[/]");

            var info = new Dictionary<string, string?>
            {
                ["Subscription ID"] = parsed.SubscriptionID,
                ["Resource Group"] = parsed.ResourceGroup,
                ["Name"] = parsed.Name,
                ["Type"] = selectedResource.Type,
                ["Location"] = parsed.Location,
                ["Kind"] = selectedResource.Kind
            };

            foreach (var kvp in info)
            {
                var value = string.IsNullOrWhiteSpace(kvp.Value) ? "N/A" : kvp.Value;
                AnsiConsole.MarkupLine($"[white]{kvp.Key}[/]: [grey]{value}[/]");
            }
        });

        return cmd;
    }
}
