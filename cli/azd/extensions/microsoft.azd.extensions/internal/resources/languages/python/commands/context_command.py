import json
import asyncio
import sys
import os
import grpc
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..")))
sys.path.insert(0, os.path.abspath(os.path.join(os.path.dirname(__file__), "..", "generated_proto")))
from azd_client import AzdClient
from google.protobuf import empty_pb2
import environment_pb2
import deployment_pb2


class ContextCommand:
    def __init__(self, azd_client: AzdClient):
        self.azd_client = azd_client

    async def execute(self):
        try:
            # User Config
            config_response = await asyncio.to_thread(
                self.azd_client.user_config.Get, empty_pb2.Empty()
            )
            if config_response and config_response.found:
                print("User Config")
                config = json.loads(config_response.value.decode("utf-8"))
                print(json.dumps(config, indent=4))

            # Project
            project_response = await asyncio.to_thread(
                self.azd_client.project.Get, empty_pb2.Empty()
            )
            if project_response:
                print("Project:")
                print(f"Name: {project_response.project.name}")
                print(f"Path: {project_response.project.path}")
            else:
                print("WARNING: No AZD project found in current directory.")
                print("Run 'azd init' to create a new project.")
                return

            # Environment
            current_env = await asyncio.to_thread(
                self.azd_client.environment.GetCurrent, empty_pb2.Empty()
            )
            if not current_env:
                print("WARNING: No AZD environment(s) found.")
                print("Run 'azd env new' to create one.")
                return

            current_env_name = current_env.environment.name
            env_list = await asyncio.to_thread(
                self.azd_client.environment.List, empty_pb2.Empty()
            )

            if not env_list.environments:
                print("No environments found.")
            else:
                print("\nEnvironments:")
                for env in env_list.environments:
                    selected = " (selected)" if env.name == current_env_name else ""
                    print(f"- {env.name}{selected}")

            # Environment values
            env_values = await asyncio.to_thread(
            self.azd_client.environment.GetValues,
            environment_pb2.GetEnvironmentRequest(name=current_env_name),
            )

            if env_values:
                print("\nEnvironment values:")
                for kv in env_values.key_values:
                    print(f"{kv.key}: {kv.value}")

            # Deployment Context
            deployment_ctx = await asyncio.to_thread(
                self.azd_client.deployment.GetDeploymentContext,
                deployment_pb2.GetDeploymentContextResponse()
            )
            if deployment_ctx:
                scope = deployment_ctx.azure_context.scope
                scope_map = {
                    "Tenant ID": scope.tenant_id,
                    "Subscription ID": scope.subscription_id,
                    "Location": scope.location,
                    "Resource Group": scope.resource_group,
                }
                print("Deployment Context:")
                for key, value in scope_map.items():
                    print(f"{key}: {value or 'N/A'}")

                print("Provisioned Azure Resources:")
                for resource_id in deployment_ctx.azure_context.resources:
                    parts = resource_id.split('/')
                    resource_name = parts[-1]
                    resource_type = f"{parts[-3]}/{parts[-2]}" if len(parts) >= 3 else "unknown"
                    print(f"- {resource_name} ({resource_type})")
        except grpc.RpcError as rpc_error:
            print(f'\nERROR: Status(StatusCode="{rpc_error.code().name}", Detail="{rpc_error.details()}")')
        except Exception as ex:
            print(f"ERROR: {ex}")
