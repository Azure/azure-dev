import asyncio  
import sys
import os
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))
from azd_client import AzdClient  
from azure.identity import DefaultAzureCredential  
from azure.mgmt.resource import ResourceManagementClient  
from rich.console import Console  
  
class PromptCommand:  
    def __init__(self, azd_client: AzdClient):  
        self._azd_client = azd_client  
  
    def build(self):  
        async def handler():  
            console = Console()  
            context = {  
                "scope": {}  
            }  
  
            # Prompt user for service choices  
            multi_select_request = {  
                "options": {  
                    "message": "Which Azure services do you use most with AZD?",  
                    "choices": [  
                        {"label": "Container Apps", "value": "container-apps"},  
                        {"label": "Functions", "value": "functions"},  
                        {"label": "Static Web Apps", "value": "static-web-apps"},  
                        {"label": "App Service", "value": "app-service"},  
                        {"label": "Cosmos DB", "value": "cosmos-db"},  
                        {"label": "SQL Database", "value": "sql-db"},  
                        {"label": "Storage", "value": "storage"},  
                        {"label": "Key Vault", "value": "key-vault"},  
                        {"label": "Kubernetes Service", "value": "kubernetes-service"},  
                    ]  
                }  
            }  
            await self._azd_client.prompt.multi_select(multi_select_request)  
  
            # Confirm whether to continue with resource selection  
            confirm_search = await self._azd_client.prompt.confirm({  

                "options": {  
                    "message": "Do you want to search for Azure resources?",  
                    "default_value": True  
                }  
            })  
            if not confirm_search.get('value'):  
                return  
  
            # Prompt subscription  
            sub_response = await self._azd_client.prompt.prompt_subscription({})  
            context["scope"]["subscription_id"] = sub_response["subscription"]["id"]  
            context["scope"]["tenant_id"] = sub_response["subscription"]["tenant_id"]  
  
            # Setup Azure credentials and clients  
            credential = DefaultAzureCredential(exclude_interactive_browser_credential=False)  
            arm_client = ResourceManagementClient(credential, context["scope"]["subscription_id"])  
            subscription = arm_client.subscriptions.get(context["scope"]["subscription_id"])  
  
            full_resource_type = None  
            # Ask to filter by resource type  
            filter_by_type = await self._azd_client.prompt.confirm({  
                "options": {  
                    "message": "Do you want to filter by resource type?",  
                    "default_value": False  
                }  
            })  
            if filter_by_type.get('value'):  
                providers = list(subscription.resource_providers.list())  
                registered_providers = [p for p in providers if p.registration_state == "Registered"]  
  
                selected_provider_index = await self._azd_client.prompt.select({  
                    "options": {  
                        "message": "Select a resource provider",  
                        "choices": [{"label": p.namespace, "value": i} for i, p in enumerate(registered_providers)]  
                    }  
                })["value"]  
  
                selected_provider = registered_providers[selected_provider_index]  
                resource_types = selected_provider.resource_types  
  
                selected_resource_type_index = await self._azd_client.prompt.select({  
                    "options": {  
                        "message": f"Select a {selected_provider.namespace} resource type",  
                        "choices": [{"label": rt.resource_type, "value": i} for i, rt in enumerate(resource_types)]  
                    }  
                })["value"]  
  
                selected_type = resource_types[selected_resource_type_index]  
                full_resource_type = f"{selected_provider.namespace}/{selected_type.resource_type}"  
  
            # Ask to filter by resource group  
            filter_by_group = await self._azd_client.prompt.confirm({  
                "options": {  
                    "message": "Do you want to filter by resource group?",  
                    "default_value": False  
                }  
            })  
            selected_resource = None  
            if filter_by_group.get('value'):  
                rg_response = await self._azd_client.prompt.prompt_resource_group({  
                    "azure_context": context  
                })  
                context["scope"]["resource_group"] = rg_response["resource_group"]["name"]  
  
                resource_group_res = await self._azd_client.prompt.prompt_resource_group_resource({  
                    "azure_context": context,  
                    "options": {  
                        "resource_type": full_resource_type,  
                        "select_options": {  
                            "allow_new_resource": False  
                        }  
                    }  
                })  
                selected_resource = resource_group_res["resource"]  
            else:  
                subscription_res = await self._azd_client.prompt.prompt_subscription_resource({  
                    "azure_context": context,  
                    "options": {  
                        "resource_type": full_resource_type,  
                        "select_options": {  
                            "allow_new_resource": False  
                        }  
                    }  
                })  
                selected_resource = subscription_res["resource"]  
  
            # Parse and display resource info  
            parsed = {  
                "subscription_id": selected_resource["id"].split('/')[2],  
                "resource_group": selected_resource["id"].split('/')[4],  
                "name": selected_resource["id"].split('/')[-1],  
                "location": selected_resource["location"]  
            }  
  
            console.print("\n[cyan]Selected resource:[/]")  
            info = {  
                "Subscription ID": parsed["subscription_id"],  
                "Resource Group": parsed["resource_group"],  
                "Name": parsed["name"],  
                "Type": selected_resource["type"],  
                "Location": parsed["location"],  
                "Kind": selected_resource.get("kind")  
            }  
            for key, value in info.items():  
                value = value if value else "N/A"  
                console.print(f"[white]{key}[/]: [grey]{value}[/]")  
  
        return handler   
                