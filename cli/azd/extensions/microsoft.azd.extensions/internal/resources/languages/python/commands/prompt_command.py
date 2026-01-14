import sys
import os
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))
from azd_client import AzdClient
from azure.identity import AzureDeveloperCliCredential
from azure.mgmt.resource import ResourceManagementClient
from rich.console import Console
import prompt_pb2

class PromptCommand:
    def __init__(self, azd_client: AzdClient):
        self._azd_client = azd_client

    async def execute(self):
        console = Console()
        context = {
            "scope": {}
        }

        # Prompt user for service choices
        multi_select_request = prompt_pb2.MultiSelectRequest(
            options=prompt_pb2.MultiSelectOptions(
                message="Which Azure services do you use most with AZD?",
                choices=[
                    prompt_pb2.MultiSelectChoice(value="container-apps", label="Container Apps", selected=False),
                    prompt_pb2.MultiSelectChoice(value="functions", label="Functions", selected=False),
                    prompt_pb2.MultiSelectChoice(value="static-web-apps", label="Static Web Apps", selected=False),
                    prompt_pb2.MultiSelectChoice(value="app-service", label="App Service", selected=False),
                    prompt_pb2.MultiSelectChoice(value="cosmos-db", label="Cosmos DB", selected=False),
                    prompt_pb2.MultiSelectChoice(value="sql-db", label="SQL Database", selected=False),
                    prompt_pb2.MultiSelectChoice(value="storage", label="Storage", selected=False),
                    prompt_pb2.MultiSelectChoice(value="key-vault", label="Key Vault", selected=False),
                    prompt_pb2.MultiSelectChoice(value="kubernetes-service", label="Kubernetes Service", selected=False)
                ],
                help_message="Please select the Azure services you use most.",
                hint="You can select multiple services.",
                display_count=5,
                display_numbers=True,
                enable_filtering=True
            )
        )

        self._azd_client.prompt.MultiSelect(multi_select_request)

        # Confirm whether to continue with resource selection
        confirm_request = prompt_pb2.ConfirmRequest(
            options = prompt_pb2.ConfirmOptions(
                message="Do you want to search for Azure resources?",
                default_value=True
            )
        )

        confirm_response = self._azd_client.prompt.Confirm(confirm_request)
        if not confirm_response.value:
            return

        # Prompt subscription
        sub_response = self._azd_client.prompt.PromptSubscription(prompt_pb2.PromptSubscriptionRequest())
        context["scope"]["subscription_id"] = sub_response.subscription.id
        context["scope"]["tenant_id"] = sub_response.subscription.tenant_id

        # Setup Azure credentials and clients
        credential = AzureDeveloperCliCredential(tenant_id=context["scope"]["tenant_id"])
        arm_client = ResourceManagementClient(credential, context["scope"]["subscription_id"])

        providers = list(arm_client.providers.list())
        full_resource_type = None
 
        # Ask to filter by resource type
        confirm_request = prompt_pb2.ConfirmRequest(
            options = prompt_pb2.ConfirmOptions(
                message="Do you want to filter by resource type?",
                default_value=False
            )
        )
        filter_by_type = self._azd_client.prompt.Confirm(confirm_request)
        if filter_by_type.value:
            registered_providers = [p for p in providers if p.registration_state == "Registered"]

            select_request = prompt_pb2.SelectRequest(
                options = prompt_pb2.SelectOptions(
                    message="Select a resource provider",
                    choices=[prompt_pb2.SelectChoice(label=p.namespace, value=str(i)) for i, p in enumerate(registered_providers)]
                )
            )
            
            selected_provider_index = (
                self._azd_client.prompt.Select(select_request)
            ).value

            selected_provider = registered_providers[selected_provider_index]
            resource_types = selected_provider.resource_types

            select_request = prompt_pb2.SelectRequest(
                options = prompt_pb2.SelectOptions(
                    message=f"Select a {selected_provider.namespace} resource type",
                    choices = [prompt_pb2.SelectChoice(label=rt.resource_type, value=str(i)) for i, rt in enumerate(resource_types)]
                )
            )
            selected_resource_type_index = (
                self._azd_client.prompt.Select(select_request)
            ).value

            selected_type = resource_types[selected_resource_type_index]
            full_resource_type = f"{selected_provider.namespace}/{selected_type.resource_type}"
            
            # Ask to filter by resource group
            confirm_request = prompt_pb2.ConfirmRequest(
                options = prompt_pb2.ConfirmOptions(
                    message="Do you want to filter by resource group?",
                    default_value=False
                )
            )
            filter_by_group = self._azd_client.prompt.Confirm(confirm_request)

            selected_resource = None
            if filter_by_group.value:
                try:
                    rg_response = self._azd_client.prompt.PromptResourceGroup(
                        prompt_pb2.PromptResourceGroupRequest(
                            azure_context=context  
                    ))
                    context["scope"]["resource_group"] = rg_response.resource_group.name

                    resource_group_res = self._azd_client.prompt.PromptResourceGroupResource(
                        prompt_pb2.PromptResourceGroupResourceRequest(
                            azure_context=context,
                            options=prompt_pb2.PromptResourceOptions(
                                resource_type=full_resource_type,
                                select_options=prompt_pb2.PromptResourceSelectOptions(
                                    allow_new_resource=False
                                )
                            )
                        )
                    )
                    print("resource_group_res22222222222222222222222", resource_group_res)
                    selected_resource = resource_group_res.resource
                except Exception as e:
                    print(f"Failed to query resources for type {full_resource_type}: {e}")
                    return
            else:
                try:
                    subscription_res = self._azd_client.prompt.PromptSubscriptionResource(
                        prompt_pb2.PromptSubscriptionResourceRequest(
                            azure_context=context,
                            options=prompt_pb2.PromptResourceOptions(
                                resource_type=full_resource_type,
                                select_options=prompt_pb2.PromptResourceSelectOptions(
                                    allow_new_resource=False
                                )
                            )
                        )
                    )
                    selected_resource = subscription_res.resource
                except Exception as e:
                    print(f"Failed to query resources for type {full_resource_type}: {e}")
                    return

            # Parse and display resource info
            parsed = {
                "subscription_id": selected_resource.id.split('/')[2],
                "resource_group": selected_resource.id.split('/')[4],
                "name": selected_resource.id.split('/')[-1],
                "location": selected_resource.location
            }

            console.print("\n[cyan]Selected resource:[/]")
            info = {
                "Subscription ID": parsed["subscription_id"],
                "Resource Group": parsed["resource_group"],
                "Name": parsed["name"],
                "Type": selected_resource.type,
                "Location": parsed["location"],
                "Kind": selected_resource.kind
            }
            for key, value in info.items():
                value = value if value else "N/A"
                console.print(f"[white]{key}[/]: [grey]{value}[/]")
