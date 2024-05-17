#!/usr/bin/env python
# coding: utf-8

import argparse
from azure.identity import AzureDeveloperCliCredential
from azure.ai.ml import MLClient, load_environment, load_model, load_online_endpoint, load_online_deployment

def create_or_update_environment(client: MLClient, file_path: str, overrides: list[dict]):
    environment = load_environment(source=file_path, params_override=overrides)
    client.environments.create_or_update(environment)

def create_or_update_model(client: MLClient, file_path: str, overrides: list[dict]):
    model = load_model(source=file_path, params_override=overrides)
    client.models.create_or_update(model)

def create_or_update_online_endpoint(client: MLClient, file_path: str, overrides: list[dict]):
    online_endpoint = load_online_endpoint(source=file_path, params_override=overrides)
    client.online_endpoints.begin_create_or_update(online_endpoint)

def create_or_update_online_deployment(client: MLClient, file_path: str, overrides: list[dict]):
    deployment = load_online_deployment(source=file_path, params_override=overrides)
    client.online_deployments.begin_create_or_update(deployment)

def main():
    parser = argparse.ArgumentParser(description='Create or update machine learning components')
    parser.add_argument('--type', '-t', type=str, help='Type of the component')
    parser.add_argument('--subscription-id', '-s', type=str, help='Azure subscription id', )
    parser.add_argument('--resource-group', '-g', type=str, help='Azure resource group')
    parser.add_argument('--workspace', '-w', type=str, help='Azure ML workspace name')
    parser.add_argument('--file', '-f', type=str, help='Path to the component spec file', required=False)
    parser.add_argument('--set', action='append', type=str, help='Set a value override', required=False)

    args = parser.parse_args()

    subscription_id: str = args.subscription_id
    resource_group: str = args.resource_group
    workspace: str = args.workspace
    overrides = [{k: v} for string in args.set for k, v in [string.split('=', 1)]]

    credential = AzureDeveloperCliCredential()
    client = MLClient(credential, subscription_id, resource_group, workspace)

    switcher = {
        'environment': create_or_update_environment,
        'model': create_or_update_model,
        'online-endpoint': create_or_update_online_endpoint,
        'online-deployment': create_or_update_online_deployment,
    }

    create_or_update_func = switcher.get(args.type)
    create_or_update_func(client, args.file, overrides)

if __name__ == '__main__':
    main()
