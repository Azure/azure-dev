import sys, os
BASE_DIR = os.path.dirname(__file__)
sys.path.insert(0, BASE_DIR)
sys.path.insert(0, os.path.join(BASE_DIR, "generated_proto"))
import asyncio
import typer
from azd_client import AzdClient
from commands.context_command import ContextCommand
from commands.listen_command import ListenCommand
from commands.prompt_command import PromptCommand

# Define the Typer app
app = typer.Typer(help="azd CLI tool", add_completion=False, context_settings={"help_option_names": ["-h", "--help"]})

# Function to retrieve environment variables and check validity
def get_azd_client() -> AzdClient:
    server_address = os.getenv("AZD_SERVER")
    access_token = os.getenv("AZD_ACCESS_TOKEN")
  
    if not server_address or not access_token:
        raise ValueError("Server address and access token must be set in environment variables AZD_SERVER and AZD_ACCESS_TOKEN.")
    
    return AzdClient(server_address, access_token)

@app.command()
def context():
    """Get the context of the AZD project & environment"""
    try:
        azd_client = get_azd_client()
        command = ContextCommand(azd_client)
        asyncio.run(command.execute())
    finally:
        azd_client.close()

@app.command()
def listen():
    """Starts the extension and listens for events"""
    loop = asyncio.new_event_loop()
    asyncio.set_event_loop(loop)

    azd_client = get_azd_client()
    command = ListenCommand(azd_client)

    try:
        loop.run_until_complete(command.execute())
    finally:
        azd_client.close()
        loop.close()

@app.command()
def prompt():
    """Examples of prompting the user for input"""
    try:
        azd_client = get_azd_client()
        command = PromptCommand(azd_client)
        asyncio.run(command.execute())
    finally:
        azd_client.close()

if __name__ == "__main__":
    app()
