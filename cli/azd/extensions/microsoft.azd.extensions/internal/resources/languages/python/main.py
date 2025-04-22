import sys, os

BASE_DIR = os.path.dirname(__file__)
# 先加项目根目录（用于找到 azd_client.py）
sys.path.insert(0, BASE_DIR)
# 再加 generated_proto 目录（用于找到 *_pb2.py/_pb2_grpc.py）
sys.path.insert(0, os.path.join(BASE_DIR, "generated_proto"))

import asyncio
from azd_client import AzdClient
from commands.context_command import ContextCommand
from commands.listen_command import ListenCommand
from commands.prompt_command import PromptCommand
import argparse

def main():  
    parser = argparse.ArgumentParser(description="azd CLI tool")  
    subparsers = parser.add_subparsers(dest="command", required=True)  
  
    # Create subcommands  
    subparsers.add_parser("context", help="Get the context of the AZD project & environment")  
    subparsers.add_parser("listen", help="Starts the extension and listens for events")  
    subparsers.add_parser("prompt", help="Examples of prompting the user for input")  
  
    args = parser.parse_args()  
  
    # Retrieve environment variables for server address and access token  
    server_address = os.getenv("AZD_SERVER")  
    access_token = os.getenv("AZD_ACCESS_TOKEN")  
  
    if not server_address or not access_token:  
        print("Server address and access token must be set in environment variables AZD_SERVER and AZD_ACCESS_TOKEN.")  
        return  
  
    azd_client = AzdClient(server_address, access_token)  
  
    # Execute the appropriate command  
    if args.command == "context":  
        command = ContextCommand(azd_client)  
        asyncio.run(command.execute())  
    elif args.command == "listen":  
        command = ListenCommand(azd_client)  
        asyncio.run(command.execute())  
    elif args.command == "prompt":  
        command = PromptCommand(azd_client)  
        asyncio.run(command.execute())  
  
    azd_client.close()  
  
if __name__ == "__main__":  
    main()  