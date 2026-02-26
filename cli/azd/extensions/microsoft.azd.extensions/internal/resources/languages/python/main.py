import sys, os
BASE_DIR = os.path.dirname(__file__)
sys.path.insert(0, BASE_DIR)
sys.path.insert(0, os.path.join(BASE_DIR, "generated_proto"))
import asyncio
import typer
import logging

# Force stdout to flush after every line
# This ensures incremental updates are immediately visible to users
sys.stdout.reconfigure(line_buffering=True)

from azd_client import AzdClient
from commands.context_command import ContextCommand
from commands.listen_command import ListenCommand
from commands.prompt_command import PromptCommand
from commands.version_command import VersionCommand

# Define the Typer app
app = typer.Typer(help="azd CLI tool", add_completion=False, context_settings={"help_option_names": ["-h", "--help"]})

# Global debug flag for entire application
debug_flag = False

# Configure logging based on debug flag
def configure_logging(debug: bool = False):
    """Configure application-wide logging based on debug flag.

    Args:
        debug: When True, enables detailed logging; otherwise, silences logs
    """
    global debug_flag
    debug_flag = debug

    if not debug:
        # No debug flag, disable logging by sending to null handler
        logging.basicConfig(level=logging.CRITICAL, handlers=[logging.NullHandler()])
        return logging.getLogger(__name__)

    try:
        # Debug flag is present, set up console logging
        formatter = logging.Formatter('[%(asctime)s] [%(levelname)s] [%(name)s] %(message)s', 
                                     datefmt='%Y-%m-%d %H:%M:%S')

        # Create console handler with formatter
        console_handler = logging.StreamHandler()
        console_handler.setFormatter(formatter)

        # Configure root logger
        root_logger = logging.getLogger()
        root_logger.setLevel(logging.INFO)

        # Remove any existing handlers to avoid duplicates
        for handler in root_logger.handlers[:]:
            root_logger.removeHandler(handler)

        root_logger.addHandler(console_handler)

        logger = logging.getLogger(__name__)
        logger.info("Debug logging enabled")
        return logger
    except Exception as ex:
        # Fall back to basic logging if something goes wrong
        logging.basicConfig(
            level=logging.INFO,
            format='[%(asctime)s] [%(levelname)s] [%(name)s] %(message)s',
            datefmt='%Y-%m-%d %H:%M:%S'
        )
        logger = logging.getLogger(__name__)
        logger.error(f"Failed to set up logging: {ex}")
        return logger

# Function to retrieve environment variables and check validity
def get_azd_client() -> AzdClient:
    server_address = os.getenv("AZD_SERVER")
    access_token = os.getenv("AZD_ACCESS_TOKEN")

    if not server_address or not access_token:
        raise ValueError("Server address and access token must be set in environment variables AZD_SERVER and AZD_ACCESS_TOKEN.")

    return AzdClient(server_address, access_token)

@app.command()
def context(debug: bool = typer.Option(False, "--debug", help="Enable debug logging")):
    """Get the context of the azd project & environment"""
    configure_logging(debug)
    try:
        azd_client = get_azd_client()
        command = ContextCommand(azd_client)
        asyncio.run(command.execute())
    finally:
        azd_client.close()

@app.command()
def listen(debug: bool = typer.Option(False, "--debug", help="Enable debug logging")):
    """Starts the extension and listens for events"""
    configure_logging(debug)
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
def prompt(debug: bool = typer.Option(False, "--debug", help="Enable debug logging")):
    """Examples of prompting the user for input"""
    configure_logging(debug)
    try:
        azd_client = get_azd_client()
        command = PromptCommand(azd_client)
        asyncio.run(command.execute())
    finally:
        azd_client.close()

@app.command()
def version():
    """Display the version of the extension"""
    try:
        azd_client = get_azd_client()
        command = VersionCommand(azd_client)
        asyncio.run(command.execute())
    finally:
        azd_client.close()

if __name__ == "__main__":
    app()
