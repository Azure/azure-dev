#!/usr/bin/env python
# coding: utf-8

import json
import argparse
from azure.identity import AzureDeveloperCliCredential
from promptflow.azure import PFClient

def create_or_update_flow(subscription_id, resource_group, workspace_name, flow_path, flow_name):
    credential = AzureDeveloperCliCredential()
    # Check if given credential can get token successfully.
    credential.get_token("https://management.azure.com/.default")

    config_path = "../config.json"
    generate_json_file(subscription_id, resource_group, workspace_name, config_path)
    pf_azure_client = PFClient.from_config(credential=credential, path=config_path)

    # Runtime no longer needed (not in flow schema)
    pf_azure_client.flows.create_or_update(
        flow=flow_path,
        display_name=flow_name,
        type="chat",
    )


def generate_json_file(subscription_id: str, resource_group: str, workspace_name: str, file_path: str):
    data = {
        "subscription_id": subscription_id,
        "resource_group": resource_group,
        "workspace_name": workspace_name
    }

    with open(file_path, 'w') as json_file:
        json.dump(data, json_file, indent=4)

parser = argparse.ArgumentParser(description='Process some integers.')
parser.add_argument('subscription_id', type=str, help='Azure subscription id')
parser.add_argument('resource_group', type=str, help='Azure resource group')
parser.add_argument('workspace_name', type=str, help='Azure workspace name')
parser.add_argument('flow_path', type=str, help='Path to the flow file')
parser.add_argument('flow_name', type=str, help='Name of the flow')

args = parser.parse_args()
create_or_update_flow(args.subscription_id, args.resource_group, args.workspace_name, args.flow_path, args.flow_name)
