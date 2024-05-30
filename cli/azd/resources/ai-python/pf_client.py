#!/usr/bin/env python
# coding: utf-8

import argparse
import json
import sys
from azure.identity import AzureDeveloperCliCredential
from promptflow.azure import PFClient

orig_stdout = sys.stdout

def create_flow(client: PFClient, file_path: str, overrides: dict = {}):
    flow = client.flows.create_or_update(file_path, **overrides)
    print(json.dumps(flow._to_dict()), file=orig_stdout)

def update_flow(client: PFClient, name: str, overrides: dict = {}):
    flow = _find_flow(client, name)
    flow = client.flows.create_or_update(flow, **overrides)
    print(json.dumps(flow._to_dict()), file=orig_stdout)

def get_flow(client: PFClient, flow_name: str):
    flow = _find_flow(client, flow_name)
    print(json.dumps(flow._to_dict()), file=orig_stdout)

def list_flows(client: PFClient):
    output = []
    flows = client.flows.list()
    for flow in flows:
        output.append(flow._to_dict())

    print(json.dumps(output), file=orig_stdout)

def _add_global_args(parser: argparse.ArgumentParser):
    parser.add_argument('--subscription-id', '-s', type=str, help='Azure subscription id', required=True)
    parser.add_argument('--resource-group', '-g', type=str, help='Azure resource group', required=True)
    parser.add_argument('--workspace', '-w', type=str, help='Azure AI workspace name', required=True)

def _find_flow(client: PFClient, flow_name: str):
    matching_flow = None
    flows = client.flows.list()
    for flow in flows:
        if flow.display_name == flow_name:
            matching_flow = flow
            break

    if matching_flow is None:
        raise ValueError(f'Flow {flow_name} not found')

    return matching_flow

def main():
    root_parser = argparse.ArgumentParser(description='Prompt flow client')

    subparsers = root_parser.add_subparsers(title='commands', dest='command')

    show_parser = subparsers.add_parser('show', help='Get a flow')
    show_parser.add_argument('--name', '-n', type=str, help='Name of the flow', required=True)

    list_parser = subparsers.add_parser('list', help='List flows')

    create_parser = subparsers.add_parser('create', help='Creates a flow')
    create_parser.add_argument('--name', '-n', type=str, help='Name of the flow', required=True)
    create_parser.add_argument('--file', '-f', type=str, help='Path to the flow file')
    create_parser.add_argument('--type', '-t', type=str, help='The flow type', default='chat')

    update_parser = subparsers.add_parser('update', help='Updates a flow')
    update_parser.add_argument('--name', '-n', type=str, help='Name of the flow', required=True)
    update_parser.add_argument('--type', '-t', type=str, help='The flow type', default='chat')

    for parser in [show_parser, list_parser, create_parser, update_parser]:
        _add_global_args(parser)

    for parser in [create_parser, update_parser]:
        parser.add_argument('--set', action='append', type=str, help='Set a value override', required=False)

    args = root_parser.parse_args()
    overrides = {}

    credential = AzureDeveloperCliCredential()
    client = PFClient(credential, args.subscription_id, args.resource_group, args.workspace)

    if (args.command == 'create' or args.command == 'update') and args.name is not None:
        if args.name is not None:
            overrides['display_name'] = args.name

        if args.type is not None:
            overrides['type'] = args.type

        if args.set is not None:
            for pair in args.set:
                key, value = pair.split('=', 1)
                overrides[key] = value

    if args.command == 'show':
        get_flow(client, args.name)
    elif args.command == 'list':
        list_flows(client)
    elif args.command == 'create':
        create_flow(client, args.file, overrides)
    elif args.command == 'update':
        update_flow(client, args.name, overrides)

if __name__ == '__main__':
    with open('output.txt', 'w') as f:
        sys.stdout = f
        main()